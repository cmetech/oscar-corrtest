package oscar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// InstalledPackAuditReader reads only complete, attributed installed-Flood audit
// observations. Even an empty successful read is not proof of a negative case.
// It does not replace the generic CorrelationAudit reader or verify installation.
type InstalledPackAuditReader struct{ client *Client }

// InstalledPackAuditRecord preserves actual audit-write time and opaque window
// attribution. Neither field establishes source event time or window bounds.
// Raw retains the original row, including fields not interpreted by this reader.
type InstalledPackAuditRecord struct {
	ID               int             `json:"id"`
	CreatedAt        time.Time       `json:"createdAt"`
	CreatedAtRaw     string          `json:"createdAtRaw"`
	AlertFingerprint string          `json:"alertFingerprint"`
	RuleID           int             `json:"ruleId"`
	RuleName         string          `json:"ruleName"`
	Pattern          string          `json:"pattern"`
	Outcome          string          `json:"outcome"`
	WindowID         string          `json:"windowId"`
	Reason           *string         `json:"reason"`
	Raw              json.RawMessage `json:"raw"`
}

func NewInstalledPackAuditReader(target domain.Target, options Options) (*InstalledPackAuditReader, error) {
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("installed audit requires an authenticated standard read target with verified TLS")
	}
	if err := (domain.TargetInput{DisplayName: "installed Flood audit", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	route, err := installedBaseURL(target.BaseURL, "/ext/mw")
	if err != nil {
		return nil, err
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("installed audit credential is empty")
	}
	client.http.Transport = installedAuditReadTransport{base: client.http.Transport, route: route}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &InstalledPackAuditReader{client: client}, nil
}

// FindAudit returns no partial records on any transport, attribution or pagination
// failure. The caller must separately evaluate expected decisions and observation
// duration; pagination is not a server-side snapshot and grants no future absence.
func (reader *InstalledPackAuditReader) FindAudit(ctx context.Context, fingerprint string, rule Rule) ([]InstalledPackAuditRecord, error) {
	if strings.TrimSpace(fingerprint) == "" || len(fingerprint) > 128 || rule.ID <= 0 || strings.TrimSpace(rule.Name) == "" || rule.Pattern != "flood" {
		return nil, fmt.Errorf("installed audit requires a fingerprint and installed Flood rule identity")
	}
	values := correlationAuditQueryValues(fingerprint)
	result := []InstalledPackAuditRecord{}
	ids := map[int]bool{}
	total := -1
	for page := 1; page <= maxListPages; page++ {
		values.Set("page", strconv.Itoa(page))
		status, raw, err := reader.client.do(ctx, http.MethodGet, correlationAuditPath, values, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("installed audit requires HTTP 200")
		}
		_, err = installedAuditObject(raw, "rows", "total", "page", "perPage")
		if err != nil {
			return nil, err
		}
		var response struct {
			Rows    []json.RawMessage `json:"rows"`
			Total   *int              `json:"total"`
			Page    *int              `json:"page"`
			PerPage *int              `json:"perPage"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Rows == nil || response.Total == nil || response.Page == nil || response.PerPage == nil {
			return nil, fmt.Errorf("installed audit lacks complete list metadata")
		}
		if *response.Total < 0 || *response.Total > maxListPages*100 || *response.Page != page || *response.PerPage != 100 || (total >= 0 && total != *response.Total) {
			return nil, fmt.Errorf("installed audit pagination is inconsistent or exceeds bound")
		}
		total = *response.Total
		expected := total - len(result)
		if expected > 100 {
			expected = 100
		}
		if expected < 0 || len(response.Rows) != expected {
			return nil, fmt.Errorf("installed audit page is incomplete")
		}
		for _, rawRow := range response.Rows {
			row, err := installedAuditRecord(rawRow, fingerprint, rule)
			if err != nil {
				return nil, err
			}
			if ids[row.ID] {
				return nil, fmt.Errorf("installed audit repeats row identity")
			}
			ids[row.ID] = true
			result = append(result, row)
		}
		if len(result) == total {
			return result, nil
		}
	}
	return nil, paginationLimitError("installed Flood audit GET")
}

func installedAuditRecord(raw json.RawMessage, fingerprint string, rule Rule) (InstalledPackAuditRecord, error) {
	var row InstalledPackAuditRecord
	_, err := installedAuditObject(raw, "id", "created_at", "alert_fingerprint", "rule_id", "rule_name", "pattern", "outcome", "window_id", "reason")
	if err != nil {
		return row, err
	}
	var wire struct {
		ID          *int    `json:"id"`
		CreatedAt   *string `json:"created_at"`
		Fingerprint *string `json:"alert_fingerprint"`
		RuleID      *int    `json:"rule_id"`
		RuleName    *string `json:"rule_name"`
		Pattern     *string `json:"pattern"`
		Outcome     *string `json:"outcome"`
		WindowID    *string `json:"window_id"`
		Reason      *string `json:"reason"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.ID == nil || *wire.ID <= 0 || wire.CreatedAt == nil || wire.Fingerprint == nil || *wire.Fingerprint != fingerprint || wire.RuleID == nil || *wire.RuleID != rule.ID || wire.RuleName == nil || *wire.RuleName != rule.Name || wire.Pattern == nil || *wire.Pattern != rule.Pattern || wire.Outcome == nil || wire.WindowID == nil || strings.TrimSpace(*wire.WindowID) == "" || len(*wire.WindowID) > 64 {
		return row, fmt.Errorf("installed audit row lacks exact complete attribution")
	}
	switch *wire.Outcome {
	case "pass_through", "enriched", "suppressed_per_notifier", "suppressed_global", "parent_emitted", "released_no_trigger":
	default:
		return row, fmt.Errorf("installed audit has unrecognized outcome")
	}
	var written time.Time
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		parsed, parseErr := time.Parse(layout, *wire.CreatedAt)
		if parseErr == nil {
			written = parsed.UTC()
			break
		}
	}
	if written.IsZero() {
		return row, fmt.Errorf("installed audit lacks a valid audit-write timestamp")
	}
	return InstalledPackAuditRecord{ID: *wire.ID, CreatedAt: written, CreatedAtRaw: *wire.CreatedAt, AlertFingerprint: *wire.Fingerprint, RuleID: *wire.RuleID, RuleName: *wire.RuleName, Pattern: *wire.Pattern, Outcome: *wire.Outcome, WindowID: *wire.WindowID, Reason: wire.Reason, Raw: append(json.RawMessage(nil), raw...)}, nil
}

// Reject duplicate keys at the two interpreted object levels rather than letting
// encoding/json silently adopt the last conflicting attribution or page value.
func installedAuditObject(raw []byte, required ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("installed audit requires a JSON object")
	}
	fields := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[strings.ToLower(key)] {
			return nil, fmt.Errorf("installed audit has ambiguous object keys")
		}
		// encoding/json also matches Unicode fold aliases such as long-s.
		for _, name := range required {
			if key != name && strings.EqualFold(key, name) {
				return nil, fmt.Errorf("installed audit has noncanonical attribution keys")
			}
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, fmt.Errorf("installed audit has invalid JSON")
		}
		fields[key] = value
		seen[strings.ToLower(key)] = true
	}
	if _, err = decoder.Token(); err != nil {
		return nil, fmt.Errorf("installed audit has invalid JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("installed audit has trailing JSON")
	}
	for _, key := range required {
		if fields[key] == nil {
			return nil, fmt.Errorf("installed audit lacks exact required JSON keys")
		}
	}
	return fields, nil
}

type installedAuditReadTransport struct {
	base  http.RoundTripper
	route installedBase
}

func (transport installedAuditReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path, err := transport.route.path(request)
	if err != nil {
		return nil, err
	}
	if request.Method != http.MethodGet || path != correlationAuditPath {
		return nil, fmt.Errorf("installed audit permits only the fixed GET")
	}
	return transport.base.RoundTrip(request)
}
