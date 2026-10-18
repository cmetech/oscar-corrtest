package oscar

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// InstalledFloodDisposition concerns only supplied identity/audit evidence. It
// is never a run verdict, permission to send, or proof of physical delivery.
type InstalledFloodDisposition string

const (
	InstalledFloodEligible   InstalledFloodDisposition = "eligible"
	InstalledFloodIncomplete InstalledFloodDisposition = "incomplete"
	InstalledFloodAmbiguous  InstalledFloodDisposition = "ambiguous"
	InstalledFloodViolation  InstalledFloodDisposition = "violation"
)

type InstalledFloodEvidence struct {
	Active  []InstalledPackActiveRecord     `json:"active"`
	History InstalledPackHistoryObservation `json:"history"`
	Audit   []InstalledPackAuditRecord      `json:"audit"`
}
type InstalledFloodEvaluation struct {
	Disposition InstalledFloodDisposition `json:"disposition"`
	Reason      string                    `json:"reason"`
}

// EvaluateInstalledFlood checks a single supplied complete observation. The runner
// must separately establish original installation/ownership, recorded sends,
// complete HTTP reads, finite observation time and retained prior failures.
// No value here certifies those obligations or exactly-once physical effects.
func EvaluateInstalledFlood(manifest InstalledPackManifest, rule Rule, evidence InstalledFloodEvidence) InstalledFloodEvaluation {
	result := func(d InstalledFloodDisposition, reason string) InstalledFloodEvaluation {
		return InstalledFloodEvaluation{d, reason}
	}
	frozen, err := FreezeInstalledPackInput(manifest.Spec())
	if err != nil || frozen != manifest || rule.ID <= 0 || rule.Name != "corrtest-operational-v1-flood" || rule.Pattern != "flood" {
		return result(InstalledFloodViolation, "fixed_contract")
	}
	count := manifest.scenario.InputCount()
	if len(evidence.Active) > count || len(evidence.Audit) > count || len(evidence.History.Records) > count {
		return result(InstalledFloodAmbiguous, "excess_identity_or_audit")
	}
	expected := map[string]ExistingFeedIdentity{}
	for i := 0; i < count; i++ {
		input, _, err := manifest.Prepare(i)
		if err != nil {
			return result(InstalledFloodViolation, "fixed_contract")
		}
		expected[input.Expected.Labels["source_id"]] = input.Expected
	}
	active := map[string]InstalledPackActiveRecord{}
	fingerprints := map[string]string{}
	for _, row := range evidence.Active {
		if len(row.Raw) > maxResponseBytes {
			return result(InstalledFloodAmbiguous, "active_representation")
		}
		parsed, err := installedActiveRecord(row.Raw, manifest.spec.QualificationRun)
		if err != nil || !reflect.DeepEqual(parsed, row) {
			return result(InstalledFloodAmbiguous, "active_representation")
		}
		source := row.Labels["source_id"]
		want, ok := expected[source]
		if !ok {
			return result(InstalledFloodViolation, "foreign_source")
		}
		if _, ok := active[source]; ok {
			return result(InstalledFloodAmbiguous, "duplicate_native_identity")
		}
		if _, ok := fingerprints[row.Fingerprint]; ok {
			return result(InstalledFloodAmbiguous, "duplicate_native_identity")
		}
		if !row.StartsAt.Equal(want.StartsAt) {
			return result(InstalledFloodViolation, "native_start")
		}
		if len(row.Labels) != len(want.Labels) {
			return result(InstalledFloodViolation, "native_labels")
		}
		for k, v := range want.Labels {
			if row.Labels[k] != v {
				return result(InstalledFloodViolation, "native_labels")
			}
		}
		active[source] = row
		fingerprints[row.Fingerprint] = source
	}
	history, err := installedFloodHistory(evidence.History, count)
	if err != nil {
		return result(InstalledFloodAmbiguous, "history_representation")
	}
	historySources := map[string]bool{}
	historyFingerprints := map[string]bool{}
	for _, row := range history {
		if strings.TrimSpace(row.Fingerprint) == "" || strings.TrimSpace(row.Labels["source_id"]) == "" || strings.TrimSpace(row.Labels["am_fingerprint"]) == "" || strings.TrimSpace(row.Labels["oscar_fingerprint"]) == "" {
			return result(InstalledFloodAmbiguous, "missing_history_attribution")
		}
		source := row.Labels["source_id"]
		want, ok := expected[source]
		if !ok {
			return result(InstalledFloodViolation, "foreign_history_source")
		}
		if historySources[source] || historyFingerprints[row.Fingerprint] {
			return result(InstalledFloodAmbiguous, "duplicate_history_episode")
		}
		historySources[source] = true
		historyFingerprints[row.Fingerprint] = true
		if row.AlertName != want.Labels["alertname"] || row.Status != "firing" || !row.StartsAt.Equal(want.StartsAt) || row.Labels["oscar_fingerprint"] != row.Fingerprint {
			return result(InstalledFloodViolation, "history_identity")
		}
		for k, v := range want.Labels {
			if !row.MatchesInputLabel(k, v) {
				return result(InstalledFloodViolation, "history_labels")
			}
		}
		if native, ok := active[source]; ok && row.Labels["am_fingerprint"] != native.Fingerprint {
			return result(InstalledFloodViolation, "history_native_link")
		}
	}
	// Inspect every supplied row before deciding that missing evidence is merely
	// incomplete. Repeated SQL identities/inserted decisions cannot be collapsed.
	auditSources := map[string]bool{}
	auditIDs := map[int]bool{}
	windows := map[string]string{}
	windowSites := map[string]string{}
	enriched := map[string]int{}
	triggered := map[string]int{}
	for _, row := range evidence.Audit {
		if len(row.Raw) > maxResponseBytes {
			return result(InstalledFloodAmbiguous, "audit_representation")
		}
		parsed, err := installedAuditRecord(row.Raw, row.AlertFingerprint, rule)
		if err != nil || !reflect.DeepEqual(parsed, row) {
			return result(InstalledFloodAmbiguous, "audit_representation")
		}
		if auditIDs[row.ID] || auditSources[row.AlertFingerprint] {
			return result(InstalledFloodAmbiguous, "duplicate_audit_decision")
		}
		auditIDs[row.ID] = true
		auditSources[row.AlertFingerprint] = true
		source, ok := fingerprints[row.AlertFingerprint]
		if !ok { // Missing Active cannot supply the Native-to-source join.
			if len(active) < count {
				continue
			}
			return result(InstalledFloodViolation, "foreign_audit_source")
		}
		site := expected[source].Labels["site"]
		if previous, ok := windows[site]; ok && previous != row.WindowID {
			return result(InstalledFloodViolation, "split_group_window")
		}
		if previous, ok := windowSites[row.WindowID]; ok && previous != site {
			return result(InstalledFloodViolation, "shared_group_window")
		}
		windows[site] = row.WindowID
		windowSites[row.WindowID] = site
		switch row.Outcome {
		case "enriched":
			enriched[site]++
		case "parent_emitted":
			triggered[site]++
		default:
			return result(InstalledFloodViolation, "unexpected_audit_outcome")
		}
	}
	// A clean cross-source contradiction is already observed even while unrelated
	// History/audit rows are pending; do not let a later poll erase that fact.
	for _, group := range manifest.scenario.Groups {
		if enriched[group.Site] > group.ExpectedEnriched || triggered[group.Site] > group.ExpectedParentEmitted {
			return result(InstalledFloodViolation, "group_decisions")
		}
	}
	if len(active) != count || len(historySources) != count || len(auditSources) != count {
		return result(InstalledFloodIncomplete, "missing_source_evidence")
	}
	for _, group := range manifest.scenario.Groups {
		if enriched[group.Site] != group.ExpectedEnriched || triggered[group.Site] != group.ExpectedParentEmitted {
			return result(InstalledFloodViolation, "group_decisions")
		}
	}
	return result(InstalledFloodEligible, "fixed_initial_audit")
}

// The fixed clean scenario has at most fifteen History rows, hence fits one
// existing per_page=100 response. More pages/rows are ambiguous, not a partial
// success. Reuse canonical validation and wire normalization; do not relax or
// add raw storage to the ordinary reader.
func installedFloodHistory(observation InstalledPackHistoryObservation, count int) ([]HistoryRecord, error) {
	if len(observation.Pages) == 0 && len(observation.Records) == 0 {
		return nil, nil
	}
	if len(observation.Pages) != 1 || len(observation.Pages[0]) > maxResponseBytes {
		return nil, fmt.Errorf("fixed History page bound")
	}
	raw := observation.Pages[0]
	if err := validateInstalledHistoryPage(raw); err != nil {
		return nil, err
	}
	var page struct {
		Records []json.RawMessage `json:"records"`
		Total   *int              `json:"total_records"`
		Pages   *int              `json:"total_pages"`
		Page    *int              `json:"page"`
		PerPage *int              `json:"per_page"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Records == nil || page.Total == nil || page.Pages == nil || page.Page == nil || page.PerPage == nil || *page.Total < 0 || *page.Total > count || *page.Total != len(page.Records) || *page.Page != 1 || *page.PerPage != 100 || *page.Pages != (*page.Total+99)/100 || len(observation.Records) != len(page.Records) {
		return nil, fmt.Errorf("incomplete History page")
	}
	result := make([]HistoryRecord, 0, len(page.Records))
	ids := map[string]bool{}
	for i, rawRow := range page.Records {
		var wire historyWire
		if json.Unmarshal(rawRow, &wire) != nil {
			return nil, fmt.Errorf("History wire")
		}
		id, ok := wire.ID.(string)
		if !ok || !existingFeedHistoryID.MatchString(id) || ids[id] {
			return nil, fmt.Errorf("History identity")
		}
		ids[id] = true
		labels := map[string]bool{}
		annotations := map[string]bool{}
		for _, label := range wire.Labels {
			if labels[label.Label] {
				return nil, fmt.Errorf("duplicate History label")
			}
			labels[label.Label] = true
		}
		for _, annotation := range wire.Annotations {
			if annotations[annotation.Annotation] {
				return nil, fmt.Errorf("duplicate History annotation")
			}
			annotations[annotation.Annotation] = true
		}
		if err := installedFloodHistoryLengths(rawRow); err != nil {
			return nil, err
		}
		normalized := wire.normalized()
		if !reflect.DeepEqual(normalized, observation.Records[i]) {
			return nil, fmt.Errorf("History typed/raw mismatch")
		}
		result = append(result, normalized)
	}
	return result, nil
}

func installedFloodHistoryLengths(raw json.RawMessage) error {
	var row struct {
		Labels []json.RawMessage `json:"labels"`
	}
	if json.Unmarshal(raw, &row) != nil {
		return fmt.Errorf("History labels")
	}
	for _, rawLabel := range row.Labels {
		fields, err := installedAuditObject(rawLabel, "Label", "Value")
		if err != nil {
			return err
		}
		// Flags are optional in the real serializer. Canonical spelling is still
		// required when present; do not permit a folded alias to hide truncation.
		var label struct {
			Label string `json:"Label"`
			Value string `json:"Value"`
		}
		if json.Unmarshal(rawLabel, &label) != nil {
			return fmt.Errorf("History label value")
		}
		switch label.Label {
		case "alertname", "qualification_pack", "qualification_run", "site", "source_id", "severity", "am_fingerprint", "oscar_fingerprint":
		default:
			continue
		}
		for key := range fields {
			for _, canonical := range []string{"value_truncated", "value_length"} {
				if key != canonical && strings.EqualFold(key, canonical) {
					return fmt.Errorf("History metadata alias")
				}
			}
		}
		if raw, ok := fields["value_truncated"]; ok {
			var truncated *bool
			if json.Unmarshal(raw, &truncated) != nil || truncated == nil || *truncated {
				return fmt.Errorf("truncated History identity")
			}
		}
		if raw, ok := fields["value_length"]; ok {
			var length *int
			if json.Unmarshal(raw, &length) != nil || length == nil || *length != utf8.RuneCountInString(label.Value) {
				return fmt.Errorf("incomplete History identity")
			}
		}
	}
	return nil
}
