package oscar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
)

const (
	validateRulePath       = "/api/v1/correlation_rules/validate"
	createRulePath         = "/api/v1/correlation_rules"
	alertsPath             = "/api/v1/alerts"
	historyPath            = "/api/v1/alerts/history"
	correlationAuditPath   = "/api/v1/correlation_rules/audit"
	notificationAuditPath  = "/api/v1/notification-audit/"
	runtimeRuleID          = "returned-rule-id"
	runtimeFingerprint     = "server-fingerprint"
	runtimeObservationFrom = "observation-start"
	runtimeObservationTo   = "observation-end"
)

// OperationPreview is one ordered, credential-free lifecycle operation.
// Duration metadata describes scheduling and is never serialized into OSCAR bodies.
type OperationPreview struct {
	Stage          string        `json:"stage"`
	CaseCode       string        `json:"caseCode,omitempty"`
	Method         string        `json:"method"`
	Path           string        `json:"path"`
	Summary        string        `json:"summary"`
	Body           string        `json:"body,omitempty"`
	Attempt        int           `json:"attempt,omitempty"`
	ScheduledDelay time.Duration `json:"scheduledDelay,omitempty"`
	RuntimeFields  []string      `json:"runtimeFields,omitempty"`
}

// BuildOperationPreview renders the same ordered OSCAR request contracts used
// by live execution without resolving a target or credential and without I/O.
func BuildOperationPreview(plan compiler.Plan, harnessVersion string) ([]OperationPreview, error) {
	if len(plan.Cases) == 0 {
		return nil, fmt.Errorf("compiled plan contains no cases")
	}
	if plan.RunID == "" || plan.ShortToken == "" {
		return nil, fmt.Errorf("compiled plan lacks preview identity")
	}
	operations := make([]OperationPreview, 0, 3+len(plan.Cases)*8+plan.MutationBudget.Alerts*3)
	appendBody := func(operation OperationPreview, body any) error {
		encoded, err := CanonicalJSON(body)
		if err != nil {
			return err
		}
		operation.Body = string(encoded)
		operations = append(operations, operation)
		return nil
	}

	if err := appendBody(OperationPreview{Stage: "preflight.validate_rule", Method: http.MethodPost, Path: validateRulePath, Summary: "Validate the first compiled rule schema before compatibility probing."}, BuildRuleRequest(plan.Cases[0].Rule, harnessVersion)); err != nil {
		return nil, err
	}
	probe, err := BuildLabelProbeAlert(plan.RunID, plan.ShortToken)
	if err != nil {
		return nil, err
	}
	probeRequest, err := BuildAlertRequest(probe)
	if err != nil {
		return nil, err
	}
	if err := appendBody(OperationPreview{Stage: "preflight.inject_label_probe", Method: http.MethodPost, Path: alertsPath, Summary: "Inject the reserved-label survival probe."}, probeRequest); err != nil {
		return nil, err
	}
	probeQuery := historyQueryValues(probe.Name, "{probe-start}", "{probe-end}")
	probeQuery.Set("page", "1")
	operations = append(operations, OperationPreview{
		Stage: "preflight.read_history", Method: http.MethodGet, Path: pathWithQuery(historyPath, probeQuery),
		Summary: "Read the probe back from authoritative OSCAR history.", RuntimeFields: []string{"probe-start", "probe-end"},
	})

	for _, item := range plan.Cases {
		ruleRequest := BuildRuleRequest(item.Rule, harnessVersion)
		if err := appendBody(OperationPreview{Stage: "setup.validate_rule", CaseCode: item.Code, Method: http.MethodPost, Path: validateRulePath, Summary: "Validate this temporary correlation rule."}, ruleRequest); err != nil {
			return nil, err
		}
		if err := appendBody(OperationPreview{Stage: "setup.create_rule", CaseCode: item.Code, Method: http.MethodPost, Path: createRulePath, Summary: "Create this temporary owned correlation rule."}, ruleRequest); err != nil {
			return nil, err
		}
	}

	for _, item := range plan.Cases {
		for index, alert := range item.Alerts {
			request, err := BuildAlertRequest(alert)
			if err != nil {
				return nil, fmt.Errorf("build %s alert attempt %d: %w", item.Code, index+1, err)
			}
			if err := appendBody(OperationPreview{
				Stage: "stimulus.inject_alert", CaseCode: item.Code, Method: http.MethodPost, Path: alertsPath,
				Summary: "Inject one compiled alert stimulus.", Attempt: index + 1, ScheduledDelay: alert.Delay,
			}, request); err != nil {
				return nil, err
			}
		}
	}

	for _, item := range plan.Cases {
		seenNames := map[string]bool{}
		for _, alert := range item.Alerts {
			if !seenNames[alert.Name] {
				operations = append(operations, historyPreview(item.Code, alert.Name, "Read source-alert history for this case."))
				seenNames[alert.Name] = true
			}
			operations = append(operations, auditPreview(item.Code))
			if item.Rule.Pattern == "parent_child" {
				operations = append(operations, notificationPreview(item.Code))
			}
		}
		if item.Rule.EmitAlertName != "" && !seenNames[item.Rule.EmitAlertName] {
			operations = append(operations, historyPreview(item.Code, item.Rule.EmitAlertName, "Read synthetic-alert history for this case."))
		}
	}
	operations = append(operations, OperationPreview{
		Stage: "evidence.persist_final_transaction", Method: "LOCAL", Path: "",
		Summary: "Persist normalized assertions and terminal evidence in the runtime's final SQLite transaction; this is not an OSCAR request.",
	})

	for _, item := range plan.Cases {
		operations = append(operations, OperationPreview{
			Stage: "cleanup.delete_rule", CaseCode: item.Code, Method: http.MethodDelete,
			Path: createRulePath + "/{returned-rule-id}", Summary: "Delete the exact rule ID returned by OSCAR.", RuntimeFields: []string{runtimeRuleID},
		})
	}
	seenResolutions := map[string]bool{}
	for _, item := range plan.Cases {
		for _, alert := range item.Alerts {
			key, err := resolutionKey(alert.Name, alert.Labels)
			if err != nil {
				return nil, err
			}
			if seenResolutions[key] {
				continue
			}
			seenResolutions[key] = true
			if err := appendResolution(&operations, item.Code, alert.Name, alert.Status, alert.Labels, alert.Annotations); err != nil {
				return nil, err
			}
		}
		if item.Rule.EmitAlertName != "" {
			labels := cloneStringMap(item.Rule.EmitLabels)
			labels["alertname"] = item.Rule.EmitAlertName
			key, err := resolutionKey(item.Rule.EmitAlertName, labels)
			if err != nil {
				return nil, err
			}
			if !seenResolutions[key] {
				seenResolutions[key] = true
				if err := appendResolution(&operations, item.Code, item.Rule.EmitAlertName, "firing", labels, nil); err != nil {
					return nil, err
				}
			}
		}
	}
	return operations, nil
}

func historyPreview(caseCode, alertName, summary string) OperationPreview {
	query := historyQueryValues(alertName, "{observation-start}", "{observation-end}")
	query.Set("page", "1")
	return OperationPreview{
		Stage: "evidence.read_history", CaseCode: caseCode, Method: http.MethodGet, Path: pathWithQuery(historyPath, query), Summary: summary,
		RuntimeFields: []string{runtimeObservationFrom, runtimeObservationTo},
	}
}

func auditPreview(caseCode string) OperationPreview {
	query := correlationAuditQueryValues("{server-fingerprint}")
	query.Set("page", "1")
	return OperationPreview{
		Stage: "evidence.read_correlation_audit", CaseCode: caseCode, Method: http.MethodGet, Path: pathWithQuery(correlationAuditPath, query),
		Summary: "Read correlation audit rows for an authoritative history fingerprint.", RuntimeFields: []string{runtimeFingerprint},
	}
}

func notificationPreview(caseCode string) OperationPreview {
	query := notificationAuditQueryValues("{server-fingerprint}", "{observation-start}", "{observation-end}")
	query.Set("page", "1")
	return OperationPreview{
		Stage: "evidence.read_notification_audit", CaseCode: caseCode, Method: http.MethodGet, Path: pathWithQuery(notificationAuditPath, query),
		Summary:       "Read notification audit rows for an authoritative history fingerprint.",
		RuntimeFields: []string{runtimeFingerprint, runtimeObservationFrom, runtimeObservationTo},
	}
}

func appendResolution(operations *[]OperationPreview, caseCode, alertName, status string, labels, annotations map[string]string) error {
	request, err := BuildResolutionRequest(HistoryRecord{
		AlertName: alertName, Fingerprint: "{server-fingerprint}", Status: status,
		Labels: labels, Annotations: annotations,
	})
	if err != nil {
		return fmt.Errorf("build %s resolution template: %w", caseCode, err)
	}
	body, err := CanonicalJSON(request)
	if err != nil {
		return err
	}
	*operations = append(*operations, OperationPreview{
		Stage: "cleanup.resolve_alert", CaseCode: caseCode, Method: http.MethodPost, Path: alertsPath,
		Summary: "Resolve an owned alert using its authoritative OSCAR history fingerprint.", Body: string(body), RuntimeFields: []string{runtimeFingerprint},
	})
	return nil
}

func resolutionKey(name string, labels map[string]string) (string, error) {
	encoded, err := CanonicalJSON(struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	}{Name: name, Labels: labels})
	return string(encoded), err
}

func historyQueryValues(alertName, start, end string) url.Values {
	filter, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"field": "alertname", "operator": "equals", "value": alertName}}})
	return url.Values{
		"perPage": {"100"}, "order": {"asc"}, "column": {"createdAt"},
		"start_datetime": {start}, "end_datetime": {end}, "filter": {string(filter)},
	}
}

func correlationAuditQueryValues(fingerprint string) url.Values {
	return url.Values{"fingerprint": {fingerprint}, "perPage": {"100"}}
}

func notificationAuditQueryValues(fingerprint, start, end string) url.Values {
	return url.Values{
		"alert_fingerprint": {fingerprint}, "per_page": {"100"},
		"date_from": {start}, "date_to": {end},
	}
}

func pathWithQuery(path string, query url.Values) string {
	return path + "?" + query.Encode()
}

func formattedTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
