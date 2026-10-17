package oscar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const installedPackPrefix = "/api/v1/packs/corrtest_operational"
const installedRulesPath = "/api/v1/correlation_rules"

var installedDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var installedLoadID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var installedRulePath = regexp.MustCompile(`^/api/v1/correlation_rules/[1-9][0-9]{0,15}$`)

// InstalledPackExpected is detached data from the verified offline kit loader.
// It does not authenticate an installation or establish exclusive target ownership.
type InstalledPackExpected struct {
	ManifestSHA256     string
	PackID             string
	Version            string
	PackContentHash    string
	MappingNamespace   string
	MappingName        string
	MappingSourceFile  string
	MappingStateSHA256 string
	MappingRowKey      string
	RuleJSON           json.RawMessage
	MappingStateJSON   json.RawMessage
}

// InstalledRuntimeBinding retains the original installed load facts. It is not
// an immutable process identity or a statement that the target stayed excluded.
type InstalledRuntimeBinding struct {
	LoadedAt        string
	LastAttemptMS   int64
	LoadedRuleIDs   []int
	Authoritative   bool
	DispatchEnabled bool
}

// InstalledPackBinding must come from a validated original installation record.
// The HTTP reader compares it, but does not validate the installer journal itself.
type InstalledPackBinding struct {
	TargetOrigin   string
	ManifestSHA256 string
	LoadID         string
	RuleID         int
	Runtime        InstalledRuntimeBinding
}

// InstalledPackObservation is a complete six-read observation, not scenario PASS.
// Even coherent readbacks cannot detect a change/use/restore between requests.
type InstalledPackObservation struct {
	Packs   json.RawMessage `json:"packs"`
	Load    json.RawMessage `json:"load"`
	Mapping json.RawMessage `json:"mapping"`
	Rules   json.RawMessage `json:"rules"`
	Rule    json.RawMessage `json:"rule"`
	Runtime json.RawMessage `json:"runtime"`
}
type InstalledPackReader struct{ client *Client }

func NewInstalledPackReader(target domain.Target, options Options) (*InstalledPackReader, error) {
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("installed preflight requires authenticated standard read target with verified TLS")
	}
	if err := (domain.TargetInput{DisplayName: "installed Flood preflight", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	origin, err := url.Parse(target.BaseURL)
	if err != nil || origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || strings.Contains(target.BaseURL, "#") {
		return nil, fmt.Errorf("installed preflight requires an origin without path, query or fragment")
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("installed preflight credential is empty")
	}
	client.http.Transport = installedPackReadTransport{client.http.Transport}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &InstalledPackReader{client: client}, nil
}

// Read uses six fixed GETs, with no polling/retry or configuration mutation.
// No partial observation is returned when any read or comparison fails.
func (reader *InstalledPackReader) Read(ctx context.Context, expected InstalledPackExpected, original InstalledPackBinding) (*InstalledPackObservation, error) {
	expected.RuleJSON = append(json.RawMessage(nil), expected.RuleJSON...)
	expected.MappingStateJSON = append(json.RawMessage(nil), expected.MappingStateJSON...)
	original.Runtime.LoadedRuleIDs = append([]int(nil), original.Runtime.LoadedRuleIDs...)
	authored, state, err := installedPackInputs(expected, original, reader.client.baseURL.String())
	if err != nil {
		return nil, err
	}
	specs := []struct {
		path  string
		query url.Values
	}{
		{"/api/v1/packs", nil},
		{installedPackPrefix + "/configuration/loads/" + original.LoadID, url.Values{"page": {"1"}, "per_page": {"100"}}},
		{installedPackPrefix + "/configuration/items/" + expected.MappingRowKey, nil},
		{installedRulesPath, url.Values{"page": {"1"}, "perPage": {"100"}, "column": {"id"}, "order": {"asc"}}},
		{installedRulesPath + "/" + strconv.Itoa(original.RuleID), nil},
		{installedRulesPath + "/runtime", url.Values{"rule_id": {strconv.Itoa(original.RuleID)}}},
	}
	raw := make([]json.RawMessage, 0, len(specs))
	for index, spec := range specs {
		status, body, err := reader.client.do(ctx, http.MethodGet, spec.path, spec.query, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("installed preflight requires HTTP 200 at read %d", index+1)
		}
		value, err := installedPackObject(body)
		if err != nil {
			return nil, fmt.Errorf("installed preflight malformed JSON at read %d", index+1)
		}
		if !installedPackMatches(index, value, expected, original, authored, state) {
			return nil, fmt.Errorf("installed preflight incomplete or changed readback at read %d", index+1)
		}
		raw = append(raw, append(json.RawMessage(nil), body...))
	}
	return &InstalledPackObservation{Packs: raw[0], Load: raw[1], Mapping: raw[2], Rules: raw[3], Rule: raw[4], Runtime: raw[5]}, nil
}

func installedPackInputs(e InstalledPackExpected, b InstalledPackBinding, origin string) (map[string]any, map[string]any, error) {
	fail := func() (map[string]any, map[string]any, error) {
		return nil, nil, fmt.Errorf("installed preflight requires exact fixed kit and original installation binding")
	}
	if b.TargetOrigin != origin || !installedDigest.MatchString(e.ManifestSHA256) || b.ManifestSHA256 != e.ManifestSHA256 || !installedLoadID.MatchString(b.LoadID) || b.RuleID <= 0 || int64(b.RuleID) > 1<<53-1 || e.PackID != "corrtest_operational" || e.Version != "0.1.0" || !strings.HasPrefix(e.PackContentHash, "sha256:") || !installedDigest.MatchString(strings.TrimPrefix(e.PackContentHash, "sha256:")) || e.MappingNamespace != "webhook_profiles" || e.MappingName != "corrtest_operational_v1" || e.MappingSourceFile != "conf/mappings/corrtest_operational_v1.json" || !installedDigest.MatchString(e.MappingStateSHA256) || !strings.HasPrefix(e.MappingRowKey, "mapping:") || !installedDigest.MatchString(strings.TrimPrefix(e.MappingRowKey, "mapping:")) {
		return fail()
	}
	if b.Runtime.Authoritative || b.Runtime.DispatchEnabled || b.Runtime.LastAttemptMS <= 0 || b.Runtime.LastAttemptMS > 1<<53-1 || len(b.Runtime.LoadedRuleIDs) != 1 || b.Runtime.LoadedRuleIDs[0] != b.RuleID || !installedPackTime(b.Runtime.LoadedAt) {
		return fail()
	}
	if len(e.RuleJSON) == 0 || len(e.RuleJSON) > 64<<10 || len(e.MappingStateJSON) == 0 || len(e.MappingStateJSON) > 256<<10 {
		return fail()
	}
	rule, err := installedPackObject(e.RuleJSON)
	if err != nil {
		return fail()
	}
	state, err := installedPackObject(e.MappingStateJSON)
	if err != nil {
		return fail()
	}
	keys := []string{"name", "pattern", "window_seconds", "group_by_labels", "match_criteria", "correlation_policy", "emit_spec", "for_notifiers", "priority", "max_synthetic_per_minute", "enabled", "description"}
	if len(rule) != len(keys) || !installedFields(rule, map[string]any{"name": "corrtest-operational-v1-flood", "pattern": "flood", "enabled": true}) {
		return fail()
	}
	for _, key := range keys {
		if _, ok := rule[key]; !ok {
			return fail()
		}
	}
	if !installedFields(state, map[string]any{"name": e.MappingName, "mapping_namespace_name": e.MappingNamespace}) {
		return fail()
	}
	return rule, state, nil
}

func installedPackMatches(index int, v map[string]any, e InstalledPackExpected, b InstalledPackBinding, authored, state map[string]any) bool {
	identity := map[string]any{"namespace": e.MappingNamespace, "name": e.MappingName}
	switch index {
	case 0:
		packs, ok := v["packs"].([]any)
		if !ok || len(packs) > 256 {
			return false
		}
		count := 0
		for _, p := range packs {
			row, ok := p.(map[string]any)
			if !ok {
				return false
			}
			if row["pack_id"] == e.PackID {
				count++
				if !installedFields(row, map[string]any{"pack_version": e.Version, "manifest_status": "valid", "pack_content_hash": e.PackContentHash}) {
					return false
				}
			}
		}
		return count == 1
	case 1:
		if !installedFields(v, map[string]any{"load_id": b.LoadID, "pack_id": e.PackID, "status": "completed", "activation_status": "succeeded", "created_count": 1, "replaced_count": 0, "skipped_count": 0, "failed_count": 0, "page": 1, "per_page": 100, "total": 1, "activation_errors": []any{}, "activation_targets": []any{map[string]any{"artifact_type": "mapping", "namespace": e.MappingNamespace}}}) {
			return false
		}
		items, ok := v["items"].([]any)
		if !ok || len(items) != 1 {
			return false
		}
		item, ok := items[0].(map[string]any)
		return ok && installedFields(item, map[string]any{"artifact_type": "mapping", "identity": identity, "source_file": e.MappingSourceFile, "source_ordinal": 0, "intended_action": "create", "result": "created", "applied_hash": e.MappingStateSHA256, "error_code": nil})
	case 2:
		return installedFields(v, map[string]any{"row_key": e.MappingRowKey, "artifact_type": "mapping", "identity": identity, "state": "up_to_date", "changed_fields": []any{}, "pack": state, "live": state, "pack_hash": e.MappingStateSHA256, "live_hash": e.MappingStateSHA256, "applied_hash": e.MappingStateSHA256})
	case 3:
		if !installedFields(v, map[string]any{"total": 1, "page": 1, "perPage": 100}) {
			return false
		}
		rows, ok := v["rows"].([]any)
		if !ok || len(rows) != 1 {
			return false
		}
		row, ok := rows[0].(map[string]any)
		return ok && installedFields(row, authored) && installedFields(row, map[string]any{"id": b.RuleID})
	case 4:
		return installedFields(v, authored) && installedFields(v, map[string]any{"id": b.RuleID})
	case 5:
		if !installedFields(v, map[string]any{"authoritative": false, "dispatch_enabled": false, "load_status": "loaded", "loaded_at": b.Runtime.LoadedAt, "last_attempt_ms": b.Runtime.LastAttemptMS, "loaded_rule_ids": b.Runtime.LoadedRuleIDs, "loaded_total": 1, "loaded_omitted": 0, "rejected_total": 0, "rejected_omitted": 0, "rejected_rules": []any{}, "selected_rule": map[string]any{"id": b.RuleID, "loaded": true, "rejection": nil}}) {
			return false
		}
		dependencies, ok := v["dependencies"].(map[string]any)
		if !ok || !installedFields(dependencies, map[string]any{"status": "ready", "nats": true, "consumer": true, "redis": true, "lua_registered": true, "timer_store": true}) {
			return false
		}
		number, ok := v["checked_at_ms"].(json.Number)
		if !ok {
			return false
		}
		ms, err := number.Int64()
		return err == nil && ms > 0 && ms <= 1<<53-1
	}
	return false
}
func installedFields(actual, want map[string]any) bool {
	for key, expected := range want {
		observed, present := actual[key]
		if !present {
			return false
		}
		a, err := json.Marshal(observed)
		if err != nil {
			return false
		}
		b, err := json.Marshal(expected)
		if err != nil || !bytes.Equal(a, b) {
			return false
		}
	}
	return true
}
func installedPackTime(raw string) bool {
	value, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && !value.IsZero()
}

// The six interpreted structures include full nested authored JSON; duplicate
// keys cannot be delegated to last-value-wins unmarshalling. This private reader
// preserves numbers and explicit nulls, caps depth, and is not a generic API.
func installedPackObject(raw []byte) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := installedPackValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return object, nil
}
func installedPackValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("JSON depth exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delim {
	case '{':
		result := map[string]any{}
		seen := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			folded := strings.Map(func(r rune) rune {
				minimum := r
				for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
					if next < minimum {
						minimum = next
					}
				}
				return minimum
			}, key)
			if len(result) >= 1024 || seen[folded] {
				return nil, fmt.Errorf("ambiguous or oversized object")
			}
			seen[folded] = true
			value, err := installedPackValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		token, err = decoder.Token()
		if err != nil || token != json.Delim('}') {
			return nil, fmt.Errorf("incomplete object")
		}
		return result, nil
	case '[':
		result := []any{}
		for decoder.More() {
			value, err := installedPackValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		token, err = decoder.Token()
		if err != nil || token != json.Delim(']') {
			return nil, fmt.Errorf("incomplete array")
		}
		return result, nil
	default:
		return nil, fmt.Errorf("invalid JSON delimiter")
	}
}

type installedPackReadTransport struct{ base http.RoundTripper }

func (transport installedPackReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.Path
	allowed := path == "/api/v1/packs" || path == installedRulesPath || path == installedRulesPath+"/runtime" || installedRulePath.MatchString(path)
	if strings.HasPrefix(path, installedPackPrefix+"/configuration/loads/") {
		allowed = installedLoadID.MatchString(strings.TrimPrefix(path, installedPackPrefix+"/configuration/loads/"))
	}
	if strings.HasPrefix(path, installedPackPrefix+"/configuration/items/mapping:") {
		allowed = installedDigest.MatchString(strings.TrimPrefix(path, installedPackPrefix+"/configuration/items/mapping:"))
	}
	if request.Method != http.MethodGet || !allowed {
		return nil, fmt.Errorf("installed preflight permits only the fixed GETs")
	}
	return transport.base.RoundTrip(request)
}
