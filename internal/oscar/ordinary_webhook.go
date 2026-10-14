package oscar

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Pins the verified mapping definition, including its exact description.
const OrdinaryProfileHash = "a823e17c4f3e3d33ffdebae28165b5835ab3255f37be8f0c6c745d8dabc92284"

type OrdinaryWebhookRequest struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
}
type ordinarySource struct {
	alert                                         compiler.AlertPlan
	forwarded, cleanupAttempted, cleanupForwarded bool
}
type ordinaryWebhookInput struct {
	mu        sync.Mutex
	binding   domain.OrdinaryWebhookBinding
	transport *Client
	sources   map[string]*ordinarySource
}

func (c *Client) configureOrdinary(target domain.Target, options Options) error {
	if target.OrdinaryWebhook == nil {
		return fmt.Errorf("ordinary webhook target binding is required")
	}
	binding := *target.OrdinaryWebhook
	if err := binding.Validate(); err != nil {
		return err
	}
	transport, err := New(domain.Target{ID: target.ID, BaseURL: binding.Endpoint, APIProfile: "public-v1", TLS: binding.TLS, Credential: binding.Credential}, Options{Getenv: options.Getenv, HarnessVersion: options.HarnessVersion, PollInterval: options.PollInterval, Now: options.Now})
	if err != nil {
		return err
	}
	transport.profilePrecondition = `"` + OrdinaryProfileHash + `"`
	c.ordinary = &ordinaryWebhookInput{binding: binding, transport: transport, sources: map[string]*ordinarySource{}}
	return nil
}
func (c *Client) InputPath() string {
	if c.ordinary != nil {
		return "ordinary-webhook"
	}
	return "alert-engine"
}
func (c *Client) InputEndpoint() string {
	if c.ordinary != nil {
		return c.ordinary.binding.Endpoint
	}
	return "POST /api/v1/alerts"
}
func (c *Client) ValidateInputBudget(plan compiler.Plan, cleanup time.Duration) error {
	if c.InputPath() != plan.InputPath && !(plan.InputPath == "" && c.InputPath() == "alert-engine") {
		return fmt.Errorf("client input route does not match scenario")
	}
	if c.ordinary != nil && time.Duration(c.ordinary.binding.ValiditySeconds)*time.Second < plan.MaxDuration+cleanup+60*time.Second {
		return fmt.Errorf("ordinary firing validity must cover scenario, 60s preflight and cleanup")
	}
	return nil
}
func BuildOrdinaryWebhookRequest(alert compiler.AlertPlan) (OrdinaryWebhookRequest, error) {
	if alert.Name == "" || alert.Labels["alertname"] != alert.Name || alert.Labels["oscar_test_run_id"] == "" || alert.StartsAt.IsZero() || !alert.EndsAt.After(alert.StartsAt) {
		return OrdinaryWebhookRequest{}, fmt.Errorf("ordinary receiver requires owned original labels and explicit start/end")
	}
	annotations := alertTransportAnnotations(alert.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	return OrdinaryWebhookRequest{Labels: cloneStringMap(alert.Labels), Annotations: annotations, StartsAt: alert.StartsAt.UTC().Format(time.RFC3339Nano), EndsAt: alert.EndsAt.UTC().Format(time.RFC3339Nano)}, nil
}
func ordinaryKey(labels map[string]string) string {
	return labels["oscar_test_run_id"] + "\x00" + labels["alertname"] + "\x00" + labels["oscar_test_event_id"]
}
func (c *Client) injectOrdinary(ctx context.Context, alert compiler.AlertPlan, record InjectionRecorder) (InjectionResult, error) {
	input := c.ordinary
	input.mu.Lock()
	defer input.mu.Unlock()
	if record == nil || alert.Status != "firing" || alert.StartsAt.IsZero() || !alert.EndsAt.IsZero() {
		return InjectionResult{}, fmt.Errorf("ordinary initial input requires recorded firing with original start and no supplied validity")
	}
	key := ordinaryKey(alert.Labels)
	if input.sources[key] != nil {
		return InjectionResult{}, fmt.Errorf("ordinary source repeat/refire is unsupported; retained input must not be replaced")
	}
	alert.EndsAt = alert.StartsAt.Add(time.Duration(input.binding.ValiditySeconds) * time.Second)
	if !alert.EndsAt.After(c.now().UTC()) {
		return InjectionResult{}, fmt.Errorf("ordinary firing validity already expired")
	}
	alert.Labels = cloneStringMap(alert.Labels)
	alert.Annotations = cloneStringMap(alert.Annotations)
	source := &ordinarySource{alert: alert}
	result, err := c.sendOrdinary(ctx, alert, func(e InjectionRequestEvidence) error {
		if err := record(e); err != nil {
			return err
		}
		input.sources[key] = source
		return nil
	})
	source.forwarded = err == nil && result.Class == InjectionForwarded
	return result, err
}
func (c *Client) sendOrdinary(ctx context.Context, alert compiler.AlertPlan, record InjectionRecorder) (InjectionResult, error) {
	if record == nil {
		return InjectionResult{}, fmt.Errorf("receiver request recorder required")
	}
	payload, err := BuildOrdinaryWebhookRequest(alert)
	if err != nil {
		return InjectionResult{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return InjectionResult{}, err
	}
	if len(body) > maxRecordedInjectionBytes {
		return InjectionResult{}, fmt.Errorf("receiver request exceeds 1 MiB")
	}
	input := c.ordinary
	sum := sha256.Sum256(body)
	evidence := InjectionRequestEvidence{Format: "corrtest-ordinary-webhook-request-v1", TargetID: c.targetID, APIProfile: domain.OrdinaryWebhookProfile, Method: http.MethodPost, Endpoint: input.binding.Endpoint, ContentType: "application/json", BodyBytes: len(body), BodySHA256: hex.EncodeToString(sum[:]), BodyBase64: base64.StdEncoding.EncodeToString(body), ProfilePrecondition: input.transport.profilePrecondition}
	if err := ctx.Err(); err != nil {
		return InjectionResult{}, err
	}
	if err := record(evidence); err != nil {
		return InjectionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return InjectionResult{}, err
	}
	endpoint, _ := url.Parse(input.binding.Endpoint)
	transport := *input.transport.http
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	status, raw, headers, err := input.transport.doBytesWithHeaders(ctx, http.MethodPost, domain.OrdinaryWebhookPath, endpoint, body, &transport)
	if err != nil {
		return InjectionResult{}, err
	}
	result := InjectionResult{Class: InjectionIndeterminate, StatusCode: status, Body: raw}
	var response struct {
		Status string `json:"status"`
	}
	if status == 200 && json.Unmarshal(raw, &response) == nil {
		if response.Status == "ok" && headers.Get("X-Oscar-Profile-Revision") == OrdinaryProfileHash {
			result.Class = InjectionForwarded
		}
		if response.Status == "acl_denied" {
			result.Class = InjectionRejected
		}
	}
	return result, nil
}

// ResolveHistoryRecorded never reconstructs a native source from downstream-enriched labels.
func (c *Client) ResolveHistoryRecorded(ctx context.Context, row HistoryRecord, record InjectionRecorder) (InjectionResult, error) {
	if c.ordinary == nil {
		return c.ResolveHistory(ctx, row)
	}
	input := c.ordinary
	input.mu.Lock()
	defer input.mu.Unlock()
	source := input.sources[ordinaryKey(row.Labels)]
	// A retained source always uses its original native identity, even if a
	// downstream label incorrectly calls it synthetic.
	if source == nil && row.Labels["oscar_test_alert_class"] == "synthetic" {
		return c.ResolveHistory(ctx, row)
	}
	if source == nil || !source.forwarded || source.cleanupAttempted || row.Fingerprint == "" || !row.StartsAt.Equal(source.alert.StartsAt) || row.AlertName != source.alert.Name {
		return InjectionResult{}, fmt.Errorf("ordinary native cleanup lacks certain original episode; reconciliation required")
	}
	for key, value := range source.alert.Labels {
		if actual, ok := row.Labels[key]; !ok || actual != value {
			return InjectionResult{}, fmt.Errorf("ordinary cleanup identity differs from retained input")
		}
	}
	clear := source.alert
	clear.Status = "resolved"
	clear.EndsAt = c.now().UTC().Truncate(time.Millisecond)
	if !clear.EndsAt.After(clear.StartsAt) {
		return InjectionResult{}, fmt.Errorf("ordinary cleanup clock does not follow original start")
	}
	clear.Annotations = cloneStringMap(clear.Annotations)
	if clear.Annotations == nil {
		clear.Annotations = map[string]string{}
	}
	clear.Annotations["oscar_test_cleanup"] = "ordinary source clear after exact original episode read-back"
	result, err := c.sendOrdinary(ctx, clear, func(e InjectionRequestEvidence) error {
		if record == nil {
			return fmt.Errorf("receiver cleanup recorder required")
		}
		if err := record(e); err != nil {
			return err
		}
		source.cleanupAttempted = true
		return nil
	})
	source.cleanupForwarded = err == nil && result.Class == InjectionForwarded
	return result, err
}

func (c *Client) UnresolvedInputCleanup() bool {
	if c.ordinary == nil {
		return false
	}
	c.ordinary.mu.Lock()
	defer c.ordinary.mu.Unlock()
	for _, source := range c.ordinary.sources {
		if !source.cleanupForwarded {
			return true
		}
	}
	return false
}

// ProbeLabelSurvivalRecorded is used only by a durable ordinary scenario run.
// The default standalone doctor retains its existing direct Alert Engine path.
func (c *Client) ProbeLabelSurvivalRecorded(ctx context.Context, runID, shortToken string, record InjectionRecorder) (LabelProbeResult, error) {
	if c.ordinary == nil {
		return c.ProbeLabelSurvival(ctx, runID, shortToken)
	}
	probe := LabelProbeResult{InputPath: c.InputPath(), Endpoint: c.InputEndpoint()}
	alert, err := BuildLabelProbeAlert(runID, shortToken)
	if err != nil {
		return probe, err
	}
	alert.StartsAt = c.now().UTC().Truncate(time.Millisecond).Add(-time.Millisecond)
	alert.Labels["oscar_test_event_id"] = runID + "-probe"
	result, err := c.InjectRecorded(ctx, alert, record)
	probe.CleanupRequired = c.UnresolvedInputCleanup()
	if err != nil {
		return probe, err
	}
	probe.Accepted = result.Class == InjectionForwarded
	if !probe.Accepted {
		return probe, fmt.Errorf("ordinary probe forwarding/profile acknowledgment unavailable")
	}
	for {
		rows, err := c.FindHistory(ctx, HistoryQuery{AlertName: alert.Name, Start: alert.StartsAt.Add(-time.Second), End: c.now().UTC().Add(time.Minute)})
		if err != nil {
			return probe, err
		}
		if len(rows) > 1 {
			return probe, fmt.Errorf("ordinary probe History is ambiguous")
		}
		if len(rows) == 1 {
			row := rows[0]
			probe.HistoryFound = true
			probe.Labels = row.Labels
			probe.Fingerprint = row.Fingerprint
			for key, value := range alert.Labels {
				if actual, ok := row.Labels[key]; !ok || actual != value {
					probe.MissingLabels = append(probe.MissingLabels, key)
				}
			}
			if row.Fingerprint == "" || row.AlertName != alert.Name || !row.StartsAt.Equal(alert.StartsAt) || len(probe.MissingLabels) > 0 {
				return probe, fmt.Errorf("ordinary probe exact native episode/labels unavailable")
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			clear, err := c.ResolveHistoryRecorded(cleanupCtx, row, record)
			probe.CleanupForwarded = err == nil && clear.Class == InjectionForwarded
			probe.CleanupRequired = c.UnresolvedInputCleanup()
			if err != nil {
				return probe, err
			}
			if !probe.CleanupForwarded {
				return probe, fmt.Errorf("ordinary probe cleanup forwarding unconfirmed")
			}
			return probe, nil
		}
		select {
		case <-ctx.Done():
			return probe, ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

func ordinaryOperationPreview(operations []OperationPreview, runID string) ([]OperationPreview, error) {
	out := make([]OperationPreview, 0, len(operations)+1)
	var probe OrdinaryWebhookRequest
	for _, op := range operations {
		if op.Path == alertsPath && op.Method == http.MethodPost {
			var group AlertGroupRequest
			if err := json.Unmarshal([]byte(op.Body), &group); err != nil || len(group.Alerts) != 1 {
				return nil, fmt.Errorf("ordinary preview requires one alert")
			}
			alert := group.Alerts[0]
			if alert.Labels["oscar_test_alert_class"] != "synthetic" {
				native := OrdinaryWebhookRequest{Labels: cloneStringMap(alert.Labels), Annotations: alert.Annotations, StartsAt: alert.StartsAt, EndsAt: "{recorded-start+configured-validitySeconds}"}
				if native.Annotations == nil {
					native.Annotations = map[string]string{}
				}
				if op.Stage == "preflight.inject_label_probe" {
					native.StartsAt = "{recorded-probe-start}"
					native.Labels["oscar_test_event_id"] = runID + "-probe"
					probe = native
				}
				if op.Stage == "cleanup.resolve_alert" {
					delete(native.Labels, "oscar_fingerprint")
					native.StartsAt = "{retained-original-start}"
					native.EndsAt = "{cleanup-now}"
					native.Annotations["oscar_test_cleanup"] = "ordinary source clear after exact original episode read-back"
				}
				body, err := CanonicalJSON(native)
				if err != nil {
					return nil, err
				}
				op.Body = string(body)
				op.Path = domain.OrdinaryWebhookPath
				op.Summary = "Use configured ordinaryWebhook endpoint and separate credential; require fixed If-Match and matching profile acknowledgment. Source cleanup requires known forwarding and exact original episode."
				op.RuntimeFields = []string{"target.ordinaryWebhook.endpoint", "recorded original startsAt", "explicit endsAt; no renewal", `If-Match: "` + OrdinaryProfileHash + `"`}
			}
		}
		out = append(out, op)
		if op.Stage == "preflight.read_history" {
			clear := probe
			clear.EndsAt = "{cleanup-now}"
			clear.Annotations = cloneStringMap(probe.Annotations)
			clear.Annotations["oscar_test_cleanup"] = "ordinary source clear after exact original episode read-back"
			body, err := CanonicalJSON(clear)
			if err != nil {
				return nil, err
			}
			out = append(out, OperationPreview{Stage: "preflight.clear_label_probe", Method: http.MethodPost, Path: domain.OrdinaryWebhookPath, Body: string(body), Summary: "Record and clear only the known-forwarded exact probe through the same pinned receiver profile; unknown remains DIRTY.", RuntimeFields: []string{"retained original labels/start", "cleanup-now", "target.ordinaryWebhook.endpoint"}})
		}
	}
	return out, nil
}
