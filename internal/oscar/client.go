package oscar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const maxResponseBytes = 4 << 20
const maxListPages = 100

type Options struct {
	Getenv         func(string) string
	HTTPClient     *http.Client
	HarnessVersion string
	PollInterval   time.Duration
}

type Client struct {
	baseURL        *url.URL
	http           *http.Client
	credential     string
	harnessVersion string
	pollInterval   time.Duration
}

func New(target domain.Target, options Options) (*Client, error) {
	if target.APIProfile != "public-v1" {
		return nil, fmt.Errorf("unsupported OSCAR API profile %q", target.APIProfile)
	}
	baseURL, err := url.Parse(target.BaseURL)
	if err != nil || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid OSCAR base URL")
	}
	credential, err := resolveCredential(target.Credential, options.Getenv)
	if err != nil {
		return nil, err
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12} // #nosec G402 -- TLS verification remains enabled unless target explicitly opts out.
		if target.TLS.Insecure {
			tlsConfig.InsecureSkipVerify = true // #nosec G402 -- explicit per-target operator choice, surfaced in sanitized metadata.
		}
		if target.TLS.CAPath != "" {
			pem, readErr := os.ReadFile(target.TLS.CAPath) // #nosec G304 -- operator-selected absolute CA path is the target contract.
			if readErr != nil {
				return nil, fmt.Errorf("read target CA: %w", readErr)
			}
			pool, poolErr := x509.SystemCertPool()
			if poolErr != nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("target CA file contains no certificates")
			}
			tlsConfig.RootCAs = pool
		}
		transport.TLSClientConfig = tlsConfig
		httpClient = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	return &Client{baseURL: baseURL, http: httpClient, credential: credential, harnessVersion: options.HarnessVersion, pollInterval: pollInterval}, nil
}

func resolveCredential(reference domain.CredentialRef, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch reference.Kind {
	case "":
		return "", nil
	case domain.CredentialEnvironment:
		value := getenv(reference.Reference)
		if value == "" {
			return "", fmt.Errorf("credential environment reference %q is unavailable", reference.Reference)
		}
		return strings.TrimSpace(value), nil
	case domain.CredentialFile, domain.CredentialSystemd:
		path := reference.Reference
		if reference.Kind == domain.CredentialSystemd {
			directory := getenv("CREDENTIALS_DIRECTORY")
			if directory == "" {
				return "", fmt.Errorf("systemd credentials directory is unavailable")
			}
			path = directory + string(os.PathSeparator) + reference.Reference
		}
		value, err := os.ReadFile(path) // #nosec G304 -- path is the explicitly configured credential reference.
		if err != nil {
			return "", fmt.Errorf("resolve credential reference: %w", err)
		}
		return strings.TrimSpace(string(value)), nil
	default:
		return "", fmt.Errorf("unsupported credential reference kind %q", reference.Kind)
	}
}

func (c *Client) ValidateRule(ctx context.Context, rule compiler.RulePlan) error {
	var response struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/correlation_rules/validate", nil, rulePayload(rule, c.harnessVersion), &response); err != nil {
		return err
	}
	if !response.Valid {
		return fmt.Errorf("OSCAR rejected correlation rule: %v", response.Errors)
	}
	return nil
}

func (c *Client) CreateRule(ctx context.Context, rule compiler.RulePlan) (Rule, error) {
	var wire ruleResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/correlation_rules", nil, rulePayload(rule, c.harnessVersion), &wire); err != nil {
		return Rule{}, err
	}
	return wire.normalized(), nil
}

func (c *Client) GetRule(ctx context.Context, id int) (Rule, error) {
	var wire ruleResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/correlation_rules/"+strconv.Itoa(id), nil, nil, &wire); err != nil {
		return Rule{}, err
	}
	return wire.normalized(), nil
}

// FindRules returns only exact-name matches after exhausting OSCAR's bounded quick-search pages.
func (c *Client) FindRules(ctx context.Context, name string) ([]Rule, error) {
	values := url.Values{"perPage": {"100"}, "column": {"id"}, "order": {"asc"}, "search": {name}}
	var result []Rule
	seen := 0
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		var response struct {
			Rows    []ruleResponse `json:"rows"`
			Total   int            `json:"total"`
			Page    int            `json:"page"`
			PerPage int            `json:"perPage"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/api/v1/correlation_rules", values, nil, &response); err != nil {
			return nil, err
		}
		seen += len(response.Rows)
		for _, item := range response.Rows {
			if item.Name == name {
				result = append(result, item.normalized())
			}
		}
		if listComplete(seen, response.Total, len(response.Rows), response.PerPage) {
			return result, nil
		}
	}
	return nil, paginationLimitError("GET /api/v1/correlation_rules")
}

func (c *Client) DeleteRule(ctx context.Context, id int) error {
	err := c.doJSON(ctx, http.MethodDelete, "/api/v1/correlation_rules/"+strconv.Itoa(id), nil, nil, nil)
	var machine *MachineError
	if errors.As(err, &machine) && machine.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *Client) Inject(ctx context.Context, alert compiler.AlertPlan) (InjectionResult, error) {
	transportFingerprint, err := alertmanagerTransportFingerprint(alert.Labels)
	if err != nil {
		return InjectionResult{}, err
	}
	payload := map[string]any{
		"receiver": "oscar-corrtest", "status": alert.Status, "groupKey": alert.Labels["oscar_test_run_id"] + ":" + alert.Name,
		"groupLabels": map[string]string{"alertname": alert.Name}, "commonLabels": alert.Labels,
		"commonAnnotations": alert.Annotations, "alerts": []any{map[string]any{"fingerPrint": transportFingerprint, "status": alert.Status, "labels": alert.Labels, "annotations": alert.Annotations}},
	}
	return c.sendAlert(ctx, payload)
}

// ResolveHistory emits one cleanup-only resolved alert using the authoritative
// OSCAR history fingerprint. User-authored plans cannot access this override.
func (c *Client) ResolveHistory(ctx context.Context, record HistoryRecord) (InjectionResult, error) {
	runID := strings.TrimSpace(record.Labels["oscar_test_run_id"])
	if record.AlertName == "" || runID == "" || strings.TrimSpace(record.Fingerprint) == "" {
		return InjectionResult{}, fmt.Errorf("history record lacks exact corrtest ownership or server fingerprint")
	}
	labels := make(map[string]string, len(record.Labels)+1)
	for key, value := range record.Labels {
		labels[key] = value
	}
	labels["alertname"] = record.AlertName
	labels["oscar_fingerprint"] = record.Fingerprint
	annotations := make(map[string]string, len(record.Annotations)+1)
	for key, value := range record.Annotations {
		annotations[key] = value
	}
	annotations["oscar_test_cleanup"] = "resolved by oscar-corrtest after exact history read-back"
	hash := sha256.Sum256([]byte("corrtest-resolve\x00" + record.Fingerprint))
	transportFingerprint := hex.EncodeToString(hash[:])[:16]
	payload := map[string]any{
		"receiver": "oscar-corrtest", "status": "resolved", "groupKey": runID + ":cleanup:" + record.AlertName,
		"groupLabels": map[string]string{"alertname": record.AlertName}, "commonLabels": labels,
		"commonAnnotations": annotations, "alerts": []any{map[string]any{"fingerPrint": transportFingerprint, "status": "resolved", "labels": labels, "annotations": annotations}},
	}
	return c.sendAlert(ctx, payload)
}

func (c *Client) sendAlert(ctx context.Context, payload map[string]any) (InjectionResult, error) {
	status, raw, err := c.do(ctx, http.MethodPost, "/api/v1/alerts", nil, payload)
	if err != nil {
		return InjectionResult{}, err
	}
	result := InjectionResult{StatusCode: status, Body: raw, Class: InjectionIndeterminate}
	var response struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		Details  string `json:"details"`
		TaskID   string `json:"task_id"`
		ID       string `json:"id"`
		Accepted *int   `json:"accepted"`
		Rejected *int   `json:"rejected"`
		Queued   *bool  `json:"queued"`
	}
	if json.Unmarshal(raw, &response) == nil {
		result.TaskID = response.TaskID
		if result.TaskID == "" {
			result.TaskID = response.ID
		}
		switch strings.ToLower(response.Status) {
		case "accepted", "scheduled", "success", "ok":
			result.Class = InjectionAccepted
		case "acl_filtered", "filtered", "rate_limited", "rejected", "dropped":
			result.Class = InjectionRejected
		case "queued", "circuit_breaker_queued":
			result.Class = InjectionQueued
		case "partial", "partially_accepted":
			result.Class = InjectionPartial
		}
		if response.ID != "" && strings.EqualFold(response.Status, "Alert group processing initiated in async mode") {
			result.Class = InjectionAccepted
		}
		if response.Accepted != nil && response.Rejected != nil && *response.Rejected > 0 {
			result.Class = InjectionPartial
		}
		if response.Queued != nil && *response.Queued {
			result.Class = InjectionQueued
		}
		text := strings.ToLower(strings.Join([]string{response.Status, response.Message, response.Details}, " "))
		switch {
		case strings.Contains(text, "alert rate limited"), strings.Contains(text, "acl filtered"), strings.Contains(text, "filtered by acl"), strings.Contains(text, "dropped"):
			result.Class = InjectionRejected
		case strings.Contains(text, "rate_limited") && (strings.Contains(text, "accepted") || strings.Contains(text, "processing may be delayed")):
			result.Class = InjectionQueued
		}
	}
	return result, nil
}

// alertmanagerTransportFingerprint satisfies OSCAR's current webhook envelope.
// It is deliberately not the OSCAR fingerprint and is never used as an assertion key.
func alertmanagerTransportFingerprint(labels map[string]string) (string, error) {
	if len(labels) == 0 {
		return "", fmt.Errorf("alert labels are required")
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key == "oscar_fingerprint" || key == "am_fingerprint" {
			return "", fmt.Errorf("pre-stamped fingerprint labels are forbidden")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		_, _ = io.WriteString(hash, key)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, labels[key])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:16], nil
}

func (c *Client) FindHistory(ctx context.Context, query HistoryQuery) ([]HistoryRecord, error) {
	filter, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"field": "alertname", "operator": "equals", "value": query.AlertName}}})
	values := url.Values{"perPage": {"100"}, "order": {"asc"}, "column": {"createdAt"},
		"start_datetime": {query.Start.UTC().Format(time.RFC3339Nano)}, "end_datetime": {query.End.UTC().Format(time.RFC3339Nano)}, "filter": {string(filter)}}
	var result []HistoryRecord
	seen := 0
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		var response historyListResponse
		if err := c.doJSON(ctx, http.MethodGet, "/api/v1/alerts/history", values, nil, &response); err != nil {
			return nil, err
		}
		seen += len(response.Records)
		for _, record := range response.Records {
			if record.AlertName != query.AlertName {
				continue
			}
			result = append(result, record.normalized())
		}
		if listComplete(seen, response.TotalRecords, len(response.Records), response.PerPage) || (response.TotalPages > 0 && page >= response.TotalPages) {
			return result, nil
		}
	}
	return nil, paginationLimitError("GET /api/v1/alerts/history")
}

func (c *Client) CorrelationAudit(ctx context.Context, fingerprint string) ([]AuditRecord, error) {
	values := url.Values{"fingerprint": {fingerprint}, "perPage": {"100"}}
	var result []AuditRecord
	seen := 0
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		var response auditListResponse
		if err := c.doJSON(ctx, http.MethodGet, "/api/v1/correlation_rules/audit", values, nil, &response); err != nil {
			return nil, err
		}
		seen += len(response.Rows)
		for _, record := range response.Rows {
			result = append(result, record.normalized())
		}
		if listComplete(seen, response.Total, len(response.Rows), response.PerPage) {
			return result, nil
		}
	}
	return nil, paginationLimitError("GET /api/v1/correlation_rules/audit")
}

func (c *Client) NotificationAudit(ctx context.Context, fingerprint string, start, end time.Time) ([]NotificationRecord, error) {
	values := url.Values{"alert_fingerprint": {fingerprint}, "per_page": {"100"},
		"date_from": {start.UTC().Format(time.RFC3339Nano)}, "date_to": {end.UTC().Format(time.RFC3339Nano)}}
	var result []NotificationRecord
	seen := 0
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		var response struct {
			Items   []notificationWire `json:"items"`
			Rows    []notificationWire `json:"rows"`
			Records []notificationWire `json:"records"`
			Total   int                `json:"total"`
			PerPage int                `json:"per_page"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/api/v1/notification-audit/", values, nil, &response); err != nil {
			return nil, err
		}
		wires := response.Items
		if len(wires) == 0 {
			wires = response.Rows
		}
		if len(wires) == 0 {
			wires = response.Records
		}
		seen += len(wires)
		for _, wire := range wires {
			if wire.AlertFingerprint == fingerprint {
				result = append(result, NotificationRecord{ID: wire.ID, AlertFingerprint: wire.AlertFingerprint, NotifierType: wire.NotifierType, Status: wire.Status, CreatedAt: wire.CreatedAt, Labels: wire.Labels})
			}
		}
		if listComplete(seen, response.Total, len(wires), response.PerPage) {
			return result, nil
		}
	}
	return nil, paginationLimitError("GET /api/v1/notification-audit/")
}

func listComplete(seen, total, pageRows, declaredPerPage int) bool {
	if total > 0 {
		return seen >= total
	}
	pageSize := declaredPerPage
	if pageSize <= 0 {
		pageSize = 100
	}
	return pageRows < pageSize
}

func paginationLimitError(operation string) error {
	return &MachineError{Operation: operation, Code: "pagination_limit", Detail: "OSCAR list exceeded the 100-page safety bound"}
}

// ProbeLabelSurvival injects one diagnostic alert and reads its server identity
// back from history. It creates no rule and never trusts the transport fingerprint.
func (c *Client) ProbeLabelSurvival(ctx context.Context, runID, shortToken string) (LabelProbeResult, error) {
	name := "CORRTEST_PROBE_P00_SOURCE_" + strings.ToUpper(shortToken)
	labels := map[string]string{
		"alertname": name, "category": "corrtest_probe", "oscar_test": "true", "oscar_test_harness": "corrtest",
		"oscar_test_schema_version": "v1", "oscar_test_run_id": runID, "oscar_test_run_short": strings.ToUpper(shortToken),
		"oscar_test_suite": "diagnostic", "oscar_test_scenario": "label-survival", "oscar_test_pattern": "probe",
		"oscar_test_case": "label-survival", "oscar_test_case_code": "P00", "oscar_test_polarity": "diagnostic",
		"oscar_test_alert_class": "source", "oscar_test_alert_role": "probe", "oscar_test_rule_name": "none", "severity": "warning",
	}
	result, err := c.Inject(ctx, compiler.AlertPlan{Name: name, Status: "firing", Labels: labels, Annotations: map[string]string{"summary": "[CORRTEST][PROBE] reserved label survival"}})
	if err != nil {
		return LabelProbeResult{}, err
	}
	probe := LabelProbeResult{Accepted: result.Class == InjectionAccepted}
	if !probe.Accepted {
		return probe, nil
	}
	start := time.Now().UTC().Add(-5 * time.Second)
	for {
		records, findErr := c.FindHistory(ctx, HistoryQuery{AlertName: name, Start: start, End: time.Now().UTC().Add(time.Minute)})
		if findErr != nil {
			return probe, findErr
		}
		if len(records) == 1 {
			probe.HistoryFound, probe.Fingerprint, probe.Labels = true, records[0].Fingerprint, records[0].Labels
			for key, value := range labels {
				if key == "severity" {
					continue
				}
				if records[0].Labels[key] != value {
					probe.MissingLabels = append(probe.MissingLabels, key)
				}
			}
			sort.Strings(probe.MissingLabels)
			return probe, nil
		}
		if len(records) > 1 {
			return probe, fmt.Errorf("label probe history identity was not unique")
		}
		select {
		case <-ctx.Done():
			return probe, ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, input, output any) error {
	_, raw, err := c.do(ctx, method, path, query, input)
	if err != nil {
		return err
	}
	if output == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(output); err != nil {
		return &MachineError{Operation: method + " " + path, Code: "invalid_json", Detail: "OSCAR returned malformed JSON"}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, input any) (int, []byte, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, nil, fmt.Errorf("encode OSCAR request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return 0, nil, fmt.Errorf("build OSCAR request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "oscar-corrtest/"+c.harnessVersion)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.credential != "" {
		request.Header.Set("X-API-Key", c.credential)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, &MachineError{Operation: method + " " + path, Code: "transport", Detail: "OSCAR request failed"}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil {
		return response.StatusCode, nil, &MachineError{Operation: method + " " + path, Code: "read", Detail: "could not read OSCAR response"}
	}
	if len(raw) > maxResponseBytes {
		return response.StatusCode, nil, &MachineError{Operation: method + " " + path, Code: "response_too_large", Detail: "OSCAR response exceeded 4 MiB"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, nil, &MachineError{Operation: method + " " + path, StatusCode: response.StatusCode, Code: "http_error", Detail: safeDetail(raw)}
	}
	return response.StatusCode, raw, nil
}

func safeDetail(raw []byte) string {
	var value struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &value) == nil && value.Detail != "" {
		if len(value.Detail) > 512 {
			return value.Detail[:512]
		}
		return value.Detail
	}
	return http.StatusText(http.StatusBadGateway)
}

func rulePayload(rule compiler.RulePlan, harnessVersion string) map[string]any {
	payload := map[string]any{"name": rule.Name, "pattern": rule.Pattern, "window_seconds": rule.WindowSeconds,
		"group_by_labels": rule.GroupBy, "match_criteria": rule.MatchCriteria, "priority": 100,
		"max_synthetic_per_minute": 10, "enabled": true, "description": rule.Description, "created_by": "oscar-corrtest/" + harnessVersion}
	if rule.EmitAlertName != "" {
		payload["emit_spec"] = map[string]any{"alertname": rule.EmitAlertName, "labels": rule.EmitLabels}
	}
	return payload
}

type ruleResponse struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
}

func (r ruleResponse) normalized() Rule {
	return Rule{ID: r.ID, Name: r.Name, Pattern: r.Pattern, Description: r.Description}
}

type labelWire struct {
	Label string `json:"Label"`
	Value string `json:"Value"`
}

type annotationWire struct {
	Annotation string `json:"Annotation"`
	Value      string `json:"Value"`
}

type historyWire struct {
	ID          any              `json:"id"`
	AlertName   string           `json:"alertname"`
	Fingerprint string           `json:"fingerprint"`
	Status      string           `json:"status"`
	CreatedAt   time.Time        `json:"createdAt"`
	Labels      []labelWire      `json:"labels"`
	Annotations []annotationWire `json:"annotations"`
}

func (r historyWire) normalized() HistoryRecord {
	result := HistoryRecord{ID: fmt.Sprint(r.ID), AlertName: r.AlertName, Fingerprint: r.Fingerprint, Status: r.Status, CreatedAt: r.CreatedAt, Labels: map[string]string{}, Annotations: map[string]string{}}
	for _, item := range r.Labels {
		result.Labels[item.Label] = item.Value
	}
	for _, item := range r.Annotations {
		result.Annotations[item.Annotation] = item.Value
	}
	return result
}

type historyListResponse struct {
	TotalRecords int           `json:"total_records"`
	TotalPages   int           `json:"total_pages"`
	Page         int           `json:"page"`
	PerPage      int           `json:"per_page"`
	Records      []historyWire `json:"records"`
}

type auditWire struct {
	ID                  int               `json:"id"`
	CreatedAt           time.Time         `json:"created_at"`
	AlertFingerprint    string            `json:"alert_fingerprint"`
	RuleID              int               `json:"rule_id"`
	RuleName            string            `json:"rule_name"`
	Pattern             string            `json:"pattern"`
	Outcome             string            `json:"outcome"`
	SuppressedNotifiers []string          `json:"suppressed_notifiers"`
	ParentFingerprint   string            `json:"parent_fingerprint"`
	AddedLabels         map[string]string `json:"added_labels"`
	Reason              string            `json:"reason"`
}

func (r auditWire) normalized() AuditRecord {
	return AuditRecord{ID: r.ID, CreatedAt: r.CreatedAt, AlertFingerprint: r.AlertFingerprint, RuleID: r.RuleID, RuleName: r.RuleName,
		Pattern: r.Pattern, Outcome: r.Outcome, SuppressedNotifiers: r.SuppressedNotifiers, ParentFingerprint: r.ParentFingerprint, AddedLabels: r.AddedLabels, Reason: r.Reason}
}

type auditListResponse struct {
	Rows    []auditWire `json:"rows"`
	Total   int         `json:"total"`
	Page    int         `json:"page"`
	PerPage int         `json:"perPage"`
}

type notificationWire struct {
	ID               string            `json:"id"`
	AlertFingerprint string            `json:"alert_fingerprint"`
	NotifierType     string            `json:"notifier_type"`
	Status           string            `json:"status"`
	CreatedAt        time.Time         `json:"created_at"`
	Labels           map[string]string `json:"labels"`
}
