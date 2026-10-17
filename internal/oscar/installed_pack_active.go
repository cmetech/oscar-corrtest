package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const installedActivePath = "/api/v1/alerts/active-alerts"

var installedNativeFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)

// InstalledPackActiveReader reads the finite installed initial-Flood run through
// the public middleware API, never through a Native publisher credential.
type InstalledPackActiveReader struct{ client *Client }

// InstalledPackActiveRecord retains Native identity, not the distinct OSCAR
// History fingerprint. The caller must compare all labels/start to frozen input.
// A successful read alone proves neither matching, delivery nor absence forever.
type InstalledPackActiveRecord struct {
	Fingerprint string            `json:"fingerprint"`
	StartsAt    time.Time         `json:"startsAt"`
	StartsAtRaw string            `json:"startsAtRaw"`
	Labels      map[string]string `json:"labels"`
	Raw         json.RawMessage   `json:"raw"`
}

func NewInstalledPackActiveReader(target domain.Target, options Options) (*InstalledPackActiveReader, error) {
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("installed Active requires authenticated standard read target with verified TLS")
	}
	if err := (domain.TargetInput{DisplayName: "installed Flood Active", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	origin, err := url.Parse(target.BaseURL)
	if err != nil || origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || strings.Contains(target.BaseURL, "#") {
		return nil, fmt.Errorf("installed Active requires an origin without path, query or fragment")
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("installed Active credential is empty")
	}
	client.http.Transport = installedActiveReadTransport{client.http.Transport}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &InstalledPackActiveReader{client: client}, nil
}

// FindActive returns a complete bounded observation or no records on error.
// The actual API exposes total_records/total_pages, but no requested-page echo.
// Stable totals, exact counts and strictly increasing fingerprints reject page
// duplication/reordering; they cannot establish an atomic server snapshot.
// An empty zero-total result is only an observation, never a passing negative.
func (reader *InstalledPackActiveReader) FindActive(ctx context.Context, run string) ([]InstalledPackActiveRecord, error) {
	if strings.TrimSpace(run) == "" || len(run) > 128 {
		return nil, fmt.Errorf("installed Active requires a bounded run identity")
	}
	filter, _ := json.Marshal(map[string]any{"logicOperator": "and", "items": []map[string]string{
		{"field": "alertname", "operator": "equals", "value": "CorrTestOperationalFlood"},
		{"field": "label:qualification_pack", "operator": "equals", "value": "corrtest_operational_v1"},
		{"field": "label:qualification_run", "operator": "equals", "value": run},
	}})
	query := url.Values{"perPage": {"100"}, "column": {"fingerprint"}, "order": {"asc"}, "show_children": {"true"}, "filter": {string(filter)}}
	result := []InstalledPackActiveRecord{}
	total := -1
	lastFingerprint := ""
	sources := map[string]bool{}
	for page := 1; page <= maxListPages; page++ {
		query.Set("page", strconv.Itoa(page))
		status, raw, err := reader.client.do(ctx, http.MethodGet, installedActivePath, query, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("installed Active requires HTTP 200")
		}
		// Reuse the strict interpreted-object check, without altering generic readers.
		if _, err = installedAuditObject(raw, "records", "total_records", "total_pages"); err != nil {
			return nil, fmt.Errorf("installed Active pagination JSON is ambiguous or incomplete")
		}
		var response struct {
			Records []json.RawMessage `json:"records"`
			Total   *int              `json:"total_records"`
			Pages   *int              `json:"total_pages"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Records == nil || response.Total == nil || response.Pages == nil || *response.Total < 0 || *response.Total > maxListPages*100 || *response.Pages != (*response.Total+99)/100 || (total >= 0 && total != *response.Total) {
			return nil, fmt.Errorf("installed Active pagination is incomplete, inconsistent or exceeds bound")
		}
		total = *response.Total
		expected := total - len(result)
		if expected > 100 {
			expected = 100
		}
		if expected < 0 || len(response.Records) != expected {
			return nil, fmt.Errorf("installed Active page is incomplete")
		}
		for _, rawRow := range response.Records {
			row, err := installedActiveRecord(rawRow, run)
			if err != nil {
				return nil, err
			}
			if row.Fingerprint <= lastFingerprint || sources[row.Labels["source_id"]] {
				return nil, fmt.Errorf("installed Active repeats or reorders source identity")
			}
			lastFingerprint = row.Fingerprint
			sources[row.Labels["source_id"]] = true
			result = append(result, row)
		}
		if len(result) == total {
			return result, nil
		}
	}
	return nil, paginationLimitError("installed Active GET")
}

func installedActiveRecord(raw json.RawMessage, run string) (InstalledPackActiveRecord, error) {
	var row InstalledPackActiveRecord
	fields, err := installedAuditObject(raw, "fingerprint", "starts_at", "alert_labels", "alertname")
	if err != nil {
		return row, fmt.Errorf("installed Active row JSON lacks exact identity")
	}
	// This optional field is also interpreted by encoding/json; a Unicode fold
	// alias must not overwrite a real parent marker with null.
	for key := range fields {
		if key != "correlation_parent_state" && strings.EqualFold(key, "correlation_parent_state") {
			return row, fmt.Errorf("installed Active has ambiguous parent attribution")
		}
	}
	var wire struct {
		Fingerprint *string           `json:"fingerprint"`
		StartsAt    *string           `json:"starts_at"`
		Labels      []json.RawMessage `json:"alert_labels"`
		AlertName   *string           `json:"alertname"`
		Parent      *string           `json:"correlation_parent_state"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Fingerprint == nil || !installedNativeFingerprint.MatchString(*wire.Fingerprint) || wire.StartsAt == nil || wire.Labels == nil || wire.AlertName == nil || *wire.AlertName != "CorrTestOperationalFlood" || wire.Parent != nil {
		return row, fmt.Errorf("installed Active row is missing, malformed or not an ordinary Native identity")
	}
	labels := map[string]string{}
	for _, rawLabel := range wire.Labels {
		if _, err := installedAuditObject(rawLabel, "name", "value"); err != nil {
			return row, fmt.Errorf("installed Active label JSON is ambiguous")
		}
		var label struct {
			Name  *string `json:"name"`
			Value *string `json:"value"`
		}
		if json.Unmarshal(rawLabel, &label) != nil || label.Name == nil || *label.Name == "" || label.Value == nil {
			return row, fmt.Errorf("installed Active label is malformed")
		}
		if _, exists := labels[*label.Name]; exists {
			return row, fmt.Errorf("installed Active repeats label name")
		}
		labels[*label.Name] = *label.Value
	}
	if labels["alertname"] != "CorrTestOperationalFlood" || labels["qualification_pack"] != "corrtest_operational_v1" || labels["qualification_run"] != run || labels["severity"] != "warning" || strings.TrimSpace(labels["source_id"]) == "" {
		return row, fmt.Errorf("installed Active returned a foreign or incomplete source")
	}
	switch labels["site"] {
	case "positive", "negative", "isolated-a", "isolated-b":
	default:
		return row, fmt.Errorf("installed Active returned a foreign source group")
	}
	var start time.Time
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		parsed, err := time.Parse(layout, *wire.StartsAt)
		if err == nil {
			start = parsed.UTC()
			break
		}
	}
	if start.IsZero() {
		return row, fmt.Errorf("installed Active lacks a valid Native start time")
	}
	return InstalledPackActiveRecord{Fingerprint: *wire.Fingerprint, StartsAt: start, StartsAtRaw: *wire.StartsAt, Labels: labels, Raw: append(json.RawMessage(nil), raw...)}, nil
}

type installedActiveReadTransport struct{ base http.RoundTripper }

func (transport installedActiveReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.Path != installedActivePath {
		return nil, fmt.Errorf("installed Active permits only the fixed GET")
	}
	return transport.base.RoundTrip(request)
}
