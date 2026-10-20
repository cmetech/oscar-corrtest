package oscar

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ShowcaseParentExpectation is one retained ordinary-pattern case. The runner
// supplies the parent/window binding from original, attributed audit evidence.
// This component does not establish source provenance, observation duration,
// deployment, delivery to an external provider, or a run-level verdict.
type ShowcaseParentExpectation struct {
	Rule                        Rule
	QualificationRun            string
	Positive                    bool
	Start, End                  time.Time
	SourceFingerprints          []string
	WindowID, ParentFingerprint string
}

func showcaseSyntheticPattern(pattern string) bool {
	switch pattern {
	case "flood", "co_occurrence", "sequence", "threshold", "cross_source", "persistence":
		return true
	}
	return false
}

// EvaluateShowcaseParentHistory requires physical History, including a complete
// original empty page for a negative observation. Eligible is only this
// component's finding; it cannot turn an audit-only run into a successful E2E run.
func EvaluateShowcaseParentHistory(want ShowcaseParentExpectation, observation InstalledPackHistoryObservation) InstalledFloodEvaluation {
	result := func(d InstalledFloodDisposition, reason string) InstalledFloodEvaluation {
		return InstalledFloodEvaluation{d, reason}
	}
	if !showcaseSyntheticPattern(want.Rule.Pattern) || want.Rule.ID <= 0 || want.Rule.Name != "corrtest-showcase-v2-"+want.Rule.Pattern || len(want.QualificationRun) != 32 || strings.Trim(want.QualificationRun, "0123456789abcdef") != "" || want.Start.IsZero() || !want.End.After(want.Start) || want.End.Sub(want.Start) > 20*time.Minute || len(want.SourceFingerprints) == 0 || len(want.SourceFingerprints) > 10 {
		return result(InstalledFloodViolation, "showcase_parent_contract")
	}
	if len(observation.Pages) != 1 {
		return result(InstalledFloodIncomplete, "showcase_parent_original_history_missing")
	}
	rows, err := installedFloodHistory(observation, 1)
	if err != nil {
		return result(InstalledFloodAmbiguous, "showcase_parent_history_representation")
	}
	if !want.Positive {
		if len(rows) > 0 {
			return result(InstalledFloodViolation, "showcase_unexpected_parent")
		}
		return result(InstalledFloodEligible, "showcase_no_parent_observed")
	}
	if len(rows) == 0 {
		return result(InstalledFloodIncomplete, "showcase_physical_parent_missing")
	}
	if strings.TrimSpace(want.WindowID) == "" || len(want.WindowID) > 64 || strings.TrimSpace(want.ParentFingerprint) == "" || len(want.ParentFingerprint) > 128 {
		return result(InstalledFloodIncomplete, "showcase_parent_audit_binding_missing")
	}
	if err := showcaseParentLengths(observation.Pages[0]); err != nil {
		return result(InstalledFloodAmbiguous, "showcase_parent_truncated_identity")
	}
	row := rows[0]
	if row.Fingerprint != want.ParentFingerprint {
		return result(InstalledFloodViolation, "showcase_parent_canonical_audit_identity")
	}
	if want.Rule.Pattern == "persistence" {
		due := want.Start.Add(30 * time.Second)
		if row.StartsAt.Before(due) || row.CreatedAt.Before(due) {
			return result(InstalledFloodViolation, "showcase_persistence_parent_before_deadline")
		}
	}
	identityJSON, _ := json.Marshal([]string{strconv.Itoa(want.Rule.ID), want.WindowID})
	identity := sha256.Sum256(identityJSON)
	expected := map[string]string{
		"alertname": "CorrTestShowcaseV2_" + want.Rule.Pattern + "_synthetic_parent", "qualification_pack": "corrtest_showcase_v2",
		"qualification_run": want.QualificationRun, "site": want.Rule.Pattern + "-positive", "severity": "critical",
		"oscar_synthetic_source": "correlator", "oscar_correlation_rule_id": strconv.Itoa(want.Rule.ID),
		"oscar_correlation_rule_name": want.Rule.Name, "oscar_correlation_window_id": want.WindowID,
		"correlation_parent_identity": fmt.Sprintf("%x", identity)[:32], "am_fingerprint": want.ParentFingerprint, "oscar_fingerprint": row.Fingerprint,
	}
	if row.Status != "firing" || row.AlertName != expected["alertname"] || strings.TrimSpace(row.Fingerprint) == "" || row.StartsAt.Before(want.Start) || row.StartsAt.After(want.End) || row.CreatedAt.Before(want.Start) || row.CreatedAt.After(want.End) {
		return result(InstalledFloodViolation, "showcase_parent_identity_or_time")
	}
	for key, value := range expected {
		if !row.MatchesInputLabel(key, value) {
			return result(InstalledFloodViolation, "showcase_parent_label_"+key)
		}
	}
	for _, source := range want.SourceFingerprints {
		if strings.TrimSpace(source) == "" || source == row.Fingerprint || source == want.ParentFingerprint {
			return result(InstalledFloodViolation, "showcase_parent_reuses_source")
		}
	}
	return result(InstalledFloodEligible, "showcase_physical_parent_bound")
}

func showcaseParentLengths(raw json.RawMessage) error {
	var page struct {
		Records []struct {
			Labels []json.RawMessage `json:"labels"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return err
	}
	for _, row := range page.Records {
		for _, rawLabel := range row.Labels {
			fields, err := installedAuditObject(rawLabel, "Label", "Value")
			if err != nil {
				return err
			}
			var label struct {
				Value string `json:"Value"`
			}
			if err = json.Unmarshal(rawLabel, &label); err != nil {
				return err
			}
			for key := range fields {
				for _, canonical := range []string{"value_length", "value_truncated"} {
					if key != canonical && strings.EqualFold(key, canonical) {
						return fmt.Errorf("metadata alias")
					}
				}
			}
			if raw, ok := fields["value_truncated"]; ok {
				var flag *bool
				if json.Unmarshal(raw, &flag) != nil || flag == nil || *flag {
					return fmt.Errorf("truncated label")
				}
			}
			if raw, ok := fields["value_length"]; ok {
				var count *int
				if json.Unmarshal(raw, &count) != nil || count == nil || *count != utf8.RuneCountInString(label.Value) {
					return fmt.Errorf("incomplete label")
				}
			}
		}
	}
	return nil
}
