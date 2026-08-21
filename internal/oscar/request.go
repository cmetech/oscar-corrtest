package oscar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
)

// RuleRequest is the OSCAR public-v1 correlation-rule request body.
type RuleRequest struct {
	Name                  string           `json:"name"`
	Pattern               string           `json:"pattern"`
	WindowSeconds         int              `json:"window_seconds"`
	GroupByLabels         []string         `json:"group_by_labels"`
	MatchCriteria         map[string]any   `json:"match_criteria"`
	Priority              int              `json:"priority"`
	MaxSyntheticPerMinute int              `json:"max_synthetic_per_minute"`
	Enabled               bool             `json:"enabled"`
	Description           string           `json:"description"`
	CreatedBy             string           `json:"created_by"`
	EmitSpec              *EmitSpecRequest `json:"emit_spec,omitempty"`
}

// EmitSpecRequest is the optional public-v1 synthetic alert specification.
type EmitSpecRequest struct {
	AlertName string            `json:"alertname"`
	Labels    map[string]string `json:"labels"`
}

// AlertGroupRequest is the Alertmanager-compatible public-v1 alert envelope.
type AlertGroupRequest struct {
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"`
	GroupKey          string            `json:"groupKey"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	Alerts            []AlertRequest    `json:"alerts"`
}

// AlertRequest is one alert in the public-v1 Alertmanager envelope.
type AlertRequest struct {
	Fingerprint string            `json:"fingerPrint"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// BuildRuleRequest converts a compiled rule to the exact body used by live execution.
func BuildRuleRequest(rule compiler.RulePlan, harnessVersion string) RuleRequest {
	request := RuleRequest{
		Name: rule.Name, Pattern: rule.Pattern, WindowSeconds: rule.WindowSeconds,
		GroupByLabels: append([]string(nil), rule.GroupBy...), MatchCriteria: cloneAnyMap(rule.MatchCriteria),
		Priority: 100, MaxSyntheticPerMinute: 10, Enabled: true,
		Description: rule.Description, CreatedBy: "oscar-corrtest/" + harnessVersion,
	}
	if rule.EmitAlertName != "" {
		request.EmitSpec = &EmitSpecRequest{AlertName: rule.EmitAlertName, Labels: cloneStringMap(rule.EmitLabels)}
	}
	return request
}

// BuildAlertRequest converts one compiled stimulus to the exact live alert body.
func BuildAlertRequest(alert compiler.AlertPlan) (AlertGroupRequest, error) {
	name := strings.TrimSpace(alert.Name)
	status := strings.TrimSpace(alert.Status)
	if name == "" || status == "" {
		return AlertGroupRequest{}, fmt.Errorf("alert name and status are required")
	}
	if len(alert.Labels) == 0 {
		return AlertGroupRequest{}, fmt.Errorf("alert labels are required")
	}
	labels := cloneStringMap(alert.Labels)
	labels["alertname"] = name
	fingerprint, err := alertmanagerTransportFingerprint(labels)
	if err != nil {
		return AlertGroupRequest{}, err
	}
	annotations := cloneStringMap(alert.Annotations)
	return AlertGroupRequest{
		Receiver: "oscar-corrtest", Status: status, GroupKey: labels["oscar_test_run_id"] + ":" + name,
		GroupLabels: map[string]string{"alertname": name}, CommonLabels: cloneStringMap(labels),
		CommonAnnotations: cloneStringMap(annotations),
		Alerts:            []AlertRequest{{Fingerprint: fingerprint, Status: status, Labels: labels, Annotations: annotations}},
	}, nil
}

// BuildResolutionRequest converts an authoritative OSCAR history record into
// the cleanup-only resolved alert body used by live execution.
func BuildResolutionRequest(record HistoryRecord) (AlertGroupRequest, error) {
	runID := strings.TrimSpace(record.Labels["oscar_test_run_id"])
	if record.AlertName == "" || runID == "" || strings.TrimSpace(record.Fingerprint) == "" {
		return AlertGroupRequest{}, fmt.Errorf("history record lacks exact corrtest ownership or server fingerprint")
	}
	labels := cloneStringMap(record.Labels)
	labels["alertname"] = record.AlertName
	labels["oscar_fingerprint"] = record.Fingerprint
	annotations := cloneStringMap(record.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["oscar_test_cleanup"] = "resolved by oscar-corrtest after exact history read-back"
	hash := sha256.Sum256([]byte("corrtest-resolve\x00" + record.Fingerprint))
	fingerprint := hex.EncodeToString(hash[:])[:16]
	return AlertGroupRequest{
		Receiver: "oscar-corrtest", Status: "resolved", GroupKey: runID + ":cleanup:" + record.AlertName,
		GroupLabels: map[string]string{"alertname": record.AlertName}, CommonLabels: cloneStringMap(labels),
		CommonAnnotations: cloneStringMap(annotations),
		Alerts:            []AlertRequest{{Fingerprint: fingerprint, Status: "resolved", Labels: labels, Annotations: annotations}},
	}, nil
}

// BuildLabelProbeAlert creates the diagnostic alert shared by preflight and preview.
func BuildLabelProbeAlert(runID, shortToken string) (compiler.AlertPlan, error) {
	runID = strings.TrimSpace(runID)
	shortToken = strings.ToUpper(strings.TrimSpace(shortToken))
	if runID == "" || shortToken == "" {
		return compiler.AlertPlan{}, fmt.Errorf("probe run ID and short token are required")
	}
	name := "CORRTEST_PROBE_P00_SOURCE_" + shortToken
	labels := map[string]string{
		"alertname": name, "category": "corrtest_probe", "oscar_test": "true", "oscar_test_harness": "corrtest",
		"oscar_test_schema_version": "v1", "oscar_test_run_id": runID, "oscar_test_run_short": shortToken,
		"oscar_test_suite": "diagnostic", "oscar_test_scenario": "label-survival", "oscar_test_pattern": "probe",
		"oscar_test_case": "label-survival", "oscar_test_case_code": "P00", "oscar_test_polarity": "diagnostic",
		"oscar_test_alert_class": "source", "oscar_test_alert_role": "probe", "oscar_test_rule_name": "none", "severity": "warning",
	}
	return compiler.AlertPlan{Name: name, Status: "firing", Labels: labels, Annotations: map[string]string{"summary": "[CORRTEST][PROBE] reserved label survival"}}, nil
}

// CanonicalJSON returns the compact deterministic JSON representation used by previews.
func CanonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize OSCAR request: %w", err)
	}
	return encoded, nil
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

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
