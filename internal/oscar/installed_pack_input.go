package oscar

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

type InstalledPackInputEvidence struct {
	ManifestSHA256 string                   `json:"manifestSha256"`
	RunID          string                   `json:"runId"`
	Index          int                      `json:"index"`
	Request        InjectionRequestEvidence `json:"request"`
	Expected       ExistingFeedIdentity     `json:"expected"`
}

type InstalledPackInputResult struct {
	InjectionResult
	ProfileRevisions []string `json:"profileRevisions"`
}

// InstalledPackInput only sends the kit's fifteen initial firings. The caller
// must durably retain UNKNOWN before send and each outcome before advancing;
// reopening requires inspecting the original journal, never a new sender.
type InstalledPackInput struct {
	mu       sync.Mutex
	bindings testpack.Bindings
	scenario testpack.Scenario
	runID    string
	startsAt time.Time
	client   *Client
	next     int
	stopped  bool
}

func NewInstalledPackInput(kit *testpack.Kit, runID string, startsAt time.Time, target domain.Target, options Options) (*InstalledPackInput, error) {
	_, offset := startsAt.Zone()
	if kit == nil || kit.Bindings().ManifestSHA256 == "" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(runID) || startsAt.Unix() <= 0 || startsAt.Year() > 9998 || startsAt.Nanosecond() != 0 || offset != 0 {
		return nil, fmt.Errorf("installed input requires verified kit, fresh run identity and whole-second UTC start")
	}
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("installed input requires explicit receiver credential and standard verified transport")
	}
	if err := (domain.TargetInput{DisplayName: "installed Flood receiver", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	if _, err := installedBaseURL(target.BaseURL, "/webhookreceiver"); err != nil {
		return nil, err
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("installed receiver credential is empty")
	}
	client.profilePrecondition = `"` + kit.Bindings().ProfileEffectiveSHA256 + `"`
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &InstalledPackInput{bindings: kit.Bindings(), scenario: kit.Scenario(), runID: runID, startsAt: startsAt, client: client}, nil
}

// Prepare returns detached evidence and exact bytes; it performs no I/O.
func (input *InstalledPackInput) Prepare(index int) (InstalledPackInputEvidence, []byte, error) {
	if input.client == nil || index < 0 || index >= input.scenario.InputCount() {
		return InstalledPackInputEvidence{}, nil, fmt.Errorf("installed input requires index 0..14")
	}
	site, position := "", index
	for _, group := range input.scenario.Groups {
		if position < group.Count {
			site = group.Site
			break
		}
		position -= group.Count
	}
	labels := map[string]string{"alertname": "CorrTestOperationalFlood", "qualification_pack": "corrtest_operational_v1", "qualification_run": input.runID, "site": site, "source_id": fmt.Sprintf("%s-%02d", input.runID, index), "severity": "warning"}
	body, err := json.Marshal(struct {
		Labels   map[string]string `json:"labels"`
		StartsAt time.Time         `json:"startsAt"`
		EndsAt   time.Time         `json:"endsAt"`
	}{labels, input.startsAt, input.startsAt.Add(time.Duration(input.scenario.Budgets.ValiditySeconds) * time.Second)})
	if err != nil {
		return InstalledPackInputEvidence{}, nil, err
	}
	sum := sha256.Sum256(body)
	path := "/api/v1/webhookreceiver/webhook/" + input.bindings.ProfileName
	if input.client.baseURL.Path == "/webhookreceiver" {
		path = "/webhook/" + input.bindings.ProfileName
	}
	request := InjectionRequestEvidence{Format: "corrtest-installed-pack-request-v1", TargetID: input.client.targetID, APIProfile: input.bindings.ProfileName, Method: http.MethodPost, Endpoint: input.client.baseURL.String() + path, ContentType: "application/json", BodyBytes: len(body), BodySHA256: hex.EncodeToString(sum[:]), BodyBase64: base64.StdEncoding.EncodeToString(body), ProfilePrecondition: input.client.profilePrecondition}
	return InstalledPackInputEvidence{ManifestSHA256: input.bindings.ManifestSHA256, RunID: input.runID, Index: index, Request: request, Expected: ExistingFeedIdentity{Labels: labels, StartsAt: input.startsAt}}, body, nil
}

// SendRecorded never retries or continues after an uncertain result, including
// a recorder error whose commit status this transport cannot know.
func (input *InstalledPackInput) SendRecorded(ctx context.Context, index int, record func(InstalledPackInputEvidence) error) (InstalledPackInputResult, error) {
	result := InstalledPackInputResult{InjectionResult: InjectionResult{Class: InjectionIndeterminate}}
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.client == nil || record == nil || input.stopped || index != input.next {
		return result, fmt.Errorf("installed input requires next unattempted input and recorder")
	}
	evidence, body, err := input.Prepare(index)
	if err != nil {
		return result, err
	}
	input.stopped = true
	if err := ctx.Err(); err != nil {
		return result, err
	}
	deadline := input.startsAt.Add(time.Duration(input.scenario.Budgets.WindowSeconds) * time.Second)
	now := input.client.now()
	remaining := deadline.Sub(now)
	if remaining <= 0 || now.Before(input.startsAt) {
		return result, fmt.Errorf("installed firing window expired or has not started")
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	if err := record(evidence); err != nil {
		return result, fmt.Errorf("record installed input before send: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	now = input.client.now()
	if now.Before(input.startsAt) || !now.Before(deadline) {
		return result, fmt.Errorf("installed firing window changed while recording")
	}
	endpoint, _ := url.Parse(evidence.Request.Endpoint)
	status, raw, headers, err := input.client.doPreparedBytesWithHeaders(ctx, http.MethodPost, endpoint.Path, endpoint, body, input.client.http, &preparedRequestHeaders{host: endpoint.Host, userAgent: "oscar-corrtest/installed-pack-v1"})
	result.StatusCode, result.Body = status, raw
	result.ProfileRevisions = append([]string(nil), headers.Values("X-Oscar-Profile-Revision")...)
	if err != nil {
		return result, err
	}
	now = input.client.now()
	if ctx.Err() != nil || now.Before(input.startsAt) || !now.Before(deadline) {
		return result, fmt.Errorf("installed acknowledgment arrived outside firing window")
	}
	fields, err := installedAuditObject(raw, "status")
	var acknowledgment string
	if err != nil || len(fields) != 1 || json.Unmarshal(fields["status"], &acknowledgment) != nil || acknowledgment != "ok" || status != http.StatusOK {
		return result, fmt.Errorf("installed receiver acknowledgment is absent or ambiguous")
	}
	revisions := result.ProfileRevisions
	if len(revisions) != 1 || revisions[0] != input.bindings.ProfileEffectiveSHA256 {
		return result, fmt.Errorf("installed receiver profile acknowledgment differs")
	}
	result.Class = InjectionForwarded
	input.next++
	input.stopped = false
	return result, nil
}
