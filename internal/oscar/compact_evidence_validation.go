package oscar

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

type CompactEvidenceDocument struct {
	Format            string               `json:"format"`
	ObservedAt        string               `json:"observed_at"`
	Scope             CompactEvidenceScope `json:"scope"`
	InventoryComplete bool                 `json:"inventory_complete"`
	GenerationCount   int                  `json:"generation_count"`
	Partition         struct {
		State   string `json:"state"`
		Version int64  `json:"version"`
	} `json:"partition"`
	Actions        []CompactEvidenceAction `json:"actions"`
	EvidenceSHA256 string                  `json:"evidence_sha256"`
}
type CompactEvidenceAction struct {
	ActionID                string `json:"action_id"`
	Generation              string `json:"generation"`
	GenerationOrdinal       int64  `json:"generation_ordinal"`
	RuleRevision            string `json:"rule_revision"`
	PhysicalHistoryVerified bool   `json:"physical_history_verified"`
	ValidationReason        string `json:"validation_reason"`
}

func compactHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
func validCompactScope(s CompactEvidenceScope) bool {
	return s.RuleID > 0 && compactHex(s.Epoch, 32) && compactHex(s.Revision, 64)
}
func compactObject(v any, keys string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	names := strings.Fields(keys)
	if !ok || len(m) != len(names) {
		return nil, false
	}
	for _, k := range names {
		if _, ok := m[k]; !ok {
			return nil, false
		}
	}
	return m, true
}
func compactString(v any, max int) bool {
	s, ok := v.(string)
	return ok && utf8.RuneCountInString(s) > 0 && utf8.RuneCountInString(s) <= max
}
func compactIsHex(v any, n int) bool { s, ok := v.(string); return ok && compactHex(s, n) }
func compactNumber(v any, min int64) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil && i >= min
}
func compactState(v any, states string) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, allowed := range strings.Fields(states) {
		if s == allowed {
			return true
		}
	}
	return false
}
func compactTime(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}
func compactInvalid() error { return fmt.Errorf("invalid compact evidence contract") }

// ValidateCompactEvidence verifies the bounded versioned wire contract and
// canonical digest without rounding 64-bit integers. The digest detects changed
// bytes; it is not a signature or an assertion that all inputs were ingested.
func ValidateCompactEvidence(raw []byte, scope CompactEvidenceScope) (*CompactEvidenceDocument, error) {
	if !validCompactScope(scope) || len(raw) > maxResponseBytes {
		return nil, compactInvalid()
	}
	// This parser rejects duplicate keys, floats, unpaired surrogates and int64
	// overflow before encoding/json can normalize or round any evidence.
	if _, err := testpack.CanonicalJSONInt64(raw); err != nil {
		return nil, compactInvalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var root map[string]any
	if err := d.Decode(&root); err != nil {
		return nil, compactInvalid()
	}
	if _, ok := compactObject(root, "format observed_at scope inventory_complete generation_count partition actions evidence_sha256"); !ok {
		return nil, compactInvalid()
	}
	observed, ok := root["observed_at"].(string)
	if !ok || !compactTime(observed) || root["format"] != "compact-evidence-v1" || root["inventory_complete"] != true {
		return nil, compactInvalid()
	}
	actual, ok := compactObject(root["scope"], "rule_id epoch revision")
	if !ok {
		return nil, compactInvalid()
	}
	id, ok := compactNumber(actual["rule_id"], 1)
	if !ok || id != scope.RuleID || actual["epoch"] != scope.Epoch || actual["revision"] != scope.Revision {
		return nil, compactInvalid()
	}
	partition, ok := compactObject(root["partition"], "state version")
	if !ok || partition["state"] != "open" {
		return nil, compactInvalid()
	}
	if _, ok = compactNumber(partition["version"], 1); !ok {
		return nil, compactInvalid()
	}
	count, ok := compactNumber(root["generation_count"], 0)
	if !ok || count > 16 {
		return nil, compactInvalid()
	}
	actions, ok := root["actions"].([]any)
	if !ok || int64(len(actions)) != count {
		return nil, compactInvalid()
	}
	seen := map[string]bool{}
	generations := map[string]bool{}
	for ordinal, rawAction := range actions {
		action, ok := rawAction.(map[string]any)
		if !ok || !validateCompactAction(action, scope, int64(ordinal+1)) {
			return nil, compactInvalid()
		}
		id := action["action_id"].(string)
		generation := action["generation"].(string)
		if seen[id] || generations[generation] {
			return nil, compactInvalid()
		}
		seen[id] = true
		generations[generation] = true
	}
	digest, ok := root["evidence_sha256"].(string)
	if !ok || !compactHex(digest, 64) {
		return nil, compactInvalid()
	}
	delete(root, "evidence_sha256")
	unsigned, err := json.Marshal(root)
	if err != nil {
		return nil, compactInvalid()
	}
	canonical, err := testpack.CanonicalJSONInt64(unsigned)
	if err != nil {
		return nil, compactInvalid()
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, compactInvalid()
	}
	var document CompactEvidenceDocument
	if err = json.Unmarshal(raw, &document); err != nil {
		return nil, compactInvalid()
	}
	return &document, nil
}

func validateCompactAction(a map[string]any, scope CompactEvidenceScope, ordinal int64) bool {
	if _, ok := compactObject(a, "action_id generation generation_ordinal rule_revision rule group_hash group_values window_id window_start_ms window_seconds frozen_count min_count enqueue_sequence intent_hash rendered_payload_hash sender ingress outbox summary_child incident parent_create_work history_receipt history physical_history_verified validation_reason"); !ok {
		return false
	}
	for _, key := range []string{"action_id", "rule_revision", "window_id", "intent_hash", "rendered_payload_hash"} {
		if !compactIsHex(a[key], 64) {
			return false
		}
	}
	if !compactIsHex(a["generation"], 32) || a["rule_revision"] != scope.Revision || !compactString(a["group_hash"], 255) {
		return false
	}
	n, ok := compactNumber(a["generation_ordinal"], 1)
	if !ok || n != ordinal {
		return false
	}
	for _, key := range []string{"window_seconds", "frozen_count", "min_count", "enqueue_sequence"} {
		if _, ok = compactNumber(a[key], 1); !ok {
			return false
		}
	}
	if _, ok = compactNumber(a["window_start_ms"], 0); !ok {
		return false
	}
	frozen, _ := compactNumber(a["frozen_count"], 1)
	minimum, _ := compactNumber(a["min_count"], 1)
	if frozen < minimum {
		return false
	}
	values, ok := a["group_values"].(map[string]any)
	if !ok || len(values) > 32 {
		return false
	}
	for k, v := range values {
		s, ok := v.(string)
		if !compactString(k, 255) || !ok || len(s) > 8192 {
			return false
		}
	}
	physical, ok := a["physical_history_verified"].(bool)
	if !ok || !compactState(a["validation_reason"], "ingress_pending summary_expansion_pending summary_retention_pending history_pending history_rejected physical_history_verified") {
		return false
	}
	schemas := map[string]string{
		"rule": "id name pattern", "sender": "state attempts", "ingress": "state", "outbox": "state publication_state", "summary_child": "child_id role publication_state", "incident": "incident_id state version content_version", "parent_create_work": "work_id state destination_state", "history_receipt": "service action_id slot_id state recovery_scope history_id", "history": "history_id fingerprint notifier_id starts_at status resolved_at accepted_content_version",
	}
	objects := map[string]map[string]any{}
	for name, keys := range schemas {
		if a[name] == nil && name != "rule" && name != "sender" {
			continue
		}
		v, ok := compactObject(a[name], keys)
		if !ok {
			return false
		}
		objects[name] = v
		for key, value := range v {
			switch {
			case name == "history" && key == "resolved_at":
				if value != nil {
					return false
				}
			case name == "parent_create_work" && key == "destination_state" || name == "history_receipt" && key == "history_id":
				if value != nil && !compactString(value, 255) {
					return false
				}
			case key == "id" || key == "attempts" || key == "version" || key == "content_version" || key == "accepted_content_version":
				if _, ok := compactNumber(value, 0); !ok {
					return false
				}
			default:
				if !compactString(value, 255) {
					return false
				}
			}
		}
	}
	rule := objects["rule"]
	ruleID, _ := compactNumber(rule["id"], 1)
	if ruleID != scope.RuleID || rule["pattern"] != "flood" {
		return false
	}
	sender := objects["sender"]
	attempts, _ := compactNumber(sender["attempts"], 0)
	if !compactState(sender["state"], "ready unknown retryable parked retained") || attempts > 100 {
		return false
	}
	states := map[string]string{"ingress": "retained retryable unavailable", "outbox": "pending", "incident": "parent_pending parent_active", "parent_create_work": "admitting pending claimed waiting_receipt complete", "history_receipt": "pending accepted rejected"}
	for name, allowed := range states {
		if v := objects[name]; v != nil && !compactState(v["state"], allowed) {
			return false
		}
	}
	for _, name := range []string{"outbox", "summary_child"} {
		if v := objects[name]; v != nil && !compactState(v["publication_state"], "unattempted unknown submitted") {
			return false
		}
	}
	for name, key := range map[string]string{"summary_child": "child_id", "incident": "incident_id", "parent_create_work": "work_id", "history_receipt": "action_id"} {
		if v := objects[name]; v != nil && !compactIsHex(v[key], 64) {
			return false
		}
	}
	child := objects["summary_child"]
	if child != nil && child["role"] != "downstream_summary_expansion" {
		return false
	}
	work := objects["parent_create_work"]
	if work != nil && work["destination_state"] != nil && !compactState(work["destination_state"], "not_started pending ambiguous accepted definitely_rejected") {
		return false
	}
	receipt := objects["history_receipt"]
	if receipt != nil && (!compactIsHex(receipt["slot_id"], 64) || receipt["service"] != "alertmanager" || receipt["recovery_scope"] != "summary") {
		return false
	}
	history := objects["history"]
	if physical {
		for name := range schemas {
			if objects[name] == nil {
				return false
			}
		}
		content, _ := compactNumber(history["accepted_content_version"], 1)
		incidentContent, _ := compactNumber(objects["incident"]["content_version"], 1)
		if a["validation_reason"] != "physical_history_verified" || receipt["state"] != "accepted" || receipt["history_id"] != history["history_id"] || history["status"] != "firing" || history["notifier_id"] != "alerthistory" || content != 1 || incidentContent != 1 || !compactTime(history["starts_at"]) {
			return false
		}
		if sender["state"] == "ready" || !compactState(objects["outbox"]["publication_state"], "unknown submitted") || !compactState(child["publication_state"], "unknown submitted") || objects["ingress"]["state"] != "retained" {
			return false
		}
	} else if history != nil || a["validation_reason"] == "physical_history_verified" {
		return false
	}
	return validateCompactStages(a, objects, physical)
}

// Only consistency visible in the projection is checked here. Private original
// intent/receipt binding remains the authenticated backend reader's authority.
func validateCompactStages(a map[string]any, objects map[string]map[string]any, physical bool) bool {
	sender, ingress, outbox, child := objects["sender"], objects["ingress"], objects["outbox"], objects["summary_child"]
	incident, work, receipt := objects["incident"], objects["parent_create_work"], objects["history_receipt"]
	attempts, _ := compactNumber(sender["attempts"], 0)
	if (sender["state"] == "ready" && attempts != 0) || (sender["state"] != "ready" && attempts == 0) {
		return false
	}
	reason := "ingress_pending"
	if ingress == nil || ingress["state"] != "retained" {
		if sender["state"] == "retained" || outbox != nil || child != nil || incident != nil || work != nil || receipt != nil {
			return false
		}
		if ingress != nil && sender["state"] == "ready" {
			return false
		}
	} else {
		if sender["state"] == "ready" || outbox == nil {
			return false
		}
		reason = "summary_expansion_pending"
		if child == nil {
			if incident != nil || work != nil || receipt != nil {
				return false
			}
		} else {
			if outbox["publication_state"] == "unattempted" {
				return false
			}
			reason = "summary_retention_pending"
			if incident == nil || work == nil {
				if incident != nil || work != nil || receipt != nil {
					return false
				}
			} else {
				if child["publication_state"] == "unattempted" {
					return false
				}
				content, _ := compactNumber(incident["content_version"], 1)
				if content != 1 {
					return false
				}
				reason = "history_pending"
				if receipt != nil {
					if receipt["slot_id"] != work["work_id"] {
						return false
					}
					if receipt["state"] == "accepted" {
						if !physical || !compactString(receipt["history_id"], 64) {
							return false
						}
						reason = "physical_history_verified"
					} else {
						if receipt["history_id"] != nil {
							return false
						}
						if receipt["state"] == "rejected" {
							reason = "history_rejected"
						}
					}
				}
				version, _ := compactNumber(incident["version"], 1)
				if physical {
					if incident["state"] != "parent_active" || version != 2 {
						return false
					}
				} else if incident["state"] != "parent_pending" || version != 1 {
					return false
				}
				destination := work["destination_state"]
				if work["state"] == "admitting" {
					if destination != nil || receipt != nil {
						return false
					}
				} else if destination == nil {
					return false
				}
				if destination == "accepted" && (receipt == nil || receipt["state"] != "accepted") {
					return false
				}
				if destination == "definitely_rejected" && (receipt == nil || receipt["state"] != "rejected") {
					return false
				}
				if work["state"] == "complete" && destination != "accepted" && destination != "definitely_rejected" {
					return false
				}
			}
		}
	}
	return a["validation_reason"] == reason
}
