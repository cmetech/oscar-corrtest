package oscar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// InstalledPackRunReport, InstalledPackPoll and InstalledPackRead are the shared
// JSON-only evidence shape. These retained assertions require independent
// original-artifact, snapshot, timing and oracle verification; decoding the DTO
// alone grants no scenario PASS, target ownership or cleanup authority.
type InstalledPackPoll struct {
	Index                   int                      `json:"index"`
	StartedAt               time.Time                `json:"startedAt"`
	EndedAt                 time.Time                `json:"endedAt"`
	Evaluation              InstalledFloodEvaluation `json:"evaluation"`
	Artifacts               []string                 `json:"artifacts"`
	ElapsedSinceLastForward time.Duration            `json:"elapsedSinceLastForwardNanos"`
}

type InstalledPackRead struct {
	Kind      string    `json:"kind"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	Artifacts []string  `json:"artifacts"`
	Error     string    `json:"error,omitempty"`
}

type InstalledPackRunReport struct {
	APIVersion       string                    `json:"apiVersion"`
	RunID            string                    `json:"runId"`
	InputPath        string                    `json:"inputPath"`
	QualificationRun string                    `json:"qualificationRun"`
	Coverage         map[string]string         `json:"coverage"`
	Stage            string                    `json:"stage"`
	StartedAt        time.Time                 `json:"startedAt"`
	EndedAt          time.Time                 `json:"endedAt"`
	Spec             *InstalledPackInputSpec   `json:"spec,omitempty"`
	Evaluation       InstalledFloodEvaluation  `json:"evaluation"`
	Adverse          *InstalledFloodEvaluation `json:"adverse,omitempty"`
	Polls            []InstalledPackPoll       `json:"polls"`
	Artifacts        []artifact.Manifest       `json:"artifacts"`
	Verdict          domain.Verdict            `json:"verdict"`
	Cleanup          domain.CleanupStatus      `json:"cleanup"`
	Failure          string                    `json:"failure,omitempty"`
	LastForwardAt    *time.Time                `json:"lastForwardAt,omitempty"`
	Reads            []InstalledPackRead       `json:"reads"`
}

// ValidateInstalledPackObservation compares six retained JSON bodies with the
// original verified kit/installation expectations. It performs no HTTP, disk,
// credentials or process checks. Caller-supplied expectations are not authority;
// HTTP status, target exclusion and immutable process identity are not proved.
func ValidateInstalledPackObservation(expected InstalledPackExpected, original InstalledPackBinding, observation InstalledPackObservation) error {
	if _, err := installedBaseURL(original.TargetOrigin, "/ext/mw"); err != nil {
		return err
	}
	authored, state, err := installedPackInputs(expected, original, original.TargetOrigin)
	if err != nil {
		return err
	}
	for index, raw := range []json.RawMessage{observation.Packs, observation.Load, observation.Mapping, observation.Rules, observation.Rule, observation.Runtime} {
		if err := validateInstalledPackRead(index, raw, expected, original, authored, state); err != nil {
			return err
		}
	}
	return nil
}
func validateInstalledPackRead(index int, raw []byte, expected InstalledPackExpected, original InstalledPackBinding, authored, state map[string]any) error {
	if len(raw) == 0 || len(raw) > maxResponseBytes {
		return fmt.Errorf("installed preflight read %d exceeds response bound", index+1)
	}
	value, err := installedPackObject(raw)
	if err != nil {
		return fmt.Errorf("installed preflight malformed JSON at read %d", index+1)
	}
	if !installedPackMatches(index, value, expected, original, authored, state) {
		return fmt.Errorf("installed preflight incomplete or changed readback at read %d", index+1)
	}
	return nil
}

// DecodeInstalledFloodEvidence recovers detached typed/raw correspondence, not
// PASS or proof of network pagination. Active/Audit artifacts contain raw rows,
// not page envelopes. Self-described token/rule fields are used solely to call
// the original row parsers; EvaluateInstalledFlood must still compare the actual
// independently verified fixed manifest and installed rule. Duplicate decisions
// and other well-formed adverse populations remain visible to that evaluator.
func DecodeInstalledFloodEvidence(activeRawRows json.RawMessage, historyPages []json.RawMessage, auditRawRows json.RawMessage) (InstalledFloodEvidence, error) {
	result := InstalledFloodEvidence{Active: []InstalledPackActiveRecord{}, Audit: []InstalledPackAuditRecord{}}
	active, err := installedEvidenceRows(activeRawRows, maxListPages*100)
	if err != nil {
		return InstalledFloodEvidence{}, err
	}
	for _, raw := range active {
		var identity struct {
			Labels []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"alert_labels"`
		}
		if json.Unmarshal(raw, &identity) != nil {
			return InstalledFloodEvidence{}, fmt.Errorf("installed evidence Active identity is invalid")
		}
		token := ""
		for _, label := range identity.Labels {
			if label.Name == "qualification_run" {
				token = label.Value
			}
		}
		if !installedQualificationToken.MatchString(token) {
			return InstalledFloodEvidence{}, fmt.Errorf("installed evidence requires fixed qualification token")
		}
		row, err := installedActiveRecord(raw, token)
		if err != nil {
			return InstalledFloodEvidence{}, err
		}
		result.Active = append(result.Active, row)
	}
	audit, err := installedEvidenceRows(auditRawRows, 15*maxListPages*100)
	if err != nil {
		return InstalledFloodEvidence{}, err
	}
	for _, raw := range audit {
		var identity struct {
			Fingerprint string `json:"alert_fingerprint"`
			RuleID      int    `json:"rule_id"`
			RuleName    string `json:"rule_name"`
			Pattern     string `json:"pattern"`
		}
		if json.Unmarshal(raw, &identity) != nil || strings.TrimSpace(identity.Fingerprint) == "" || len(identity.Fingerprint) > 128 || identity.RuleID <= 0 || strings.TrimSpace(identity.RuleName) == "" || identity.Pattern != "flood" {
			return InstalledFloodEvidence{}, fmt.Errorf("installed evidence audit identity is invalid")
		}
		row, err := installedAuditRecord(raw, identity.Fingerprint, Rule{ID: identity.RuleID, Name: identity.RuleName, Pattern: identity.Pattern})
		if err != nil {
			return InstalledFloodEvidence{}, err
		}
		result.Audit = append(result.Audit, row)
	}
	if len(historyPages) == 0 || len(historyPages) > maxListPages {
		return InstalledFloodEvidence{}, fmt.Errorf("installed evidence requires bounded complete History pages")
	}
	state := newBoundedHistoryState()
	totalBytes := 0
	result.History.Records = []HistoryRecord{}
	for index, raw := range historyPages {
		if len(raw) == 0 || len(raw) > maxResponseBytes || len(raw) > installedHistoryMaxBytes-totalBytes {
			return InstalledFloodEvidence{}, fmt.Errorf("installed evidence History byte bound")
		}
		totalBytes += len(raw)
		if err := validateInstalledHistoryPage(raw); err != nil {
			return InstalledFloodEvidence{}, err
		}
		rows, done, err := state.decode(raw, index+1, "CorrTestOperationalFlood")
		if err != nil {
			return InstalledFloodEvidence{}, err
		}
		if done != (index == len(historyPages)-1) {
			return InstalledFloodEvidence{}, fmt.Errorf("installed evidence History pages are incomplete or extra")
		}
		result.History.Records = append(result.History.Records, rows...)
		result.History.Pages = append(result.History.Pages, append(json.RawMessage(nil), raw...))
	}
	return result, nil
}

func installedEvidenceRows(raw []byte, maxRows int) ([]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > installedHistoryMaxBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("installed raw-row bytes exceed bound or are invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("installed evidence requires a row array")
	}
	result := []json.RawMessage{}
	for decoder.More() {
		if len(result) >= maxRows {
			return nil, fmt.Errorf("installed row count exceeds bound")
		}
		var row json.RawMessage
		if decoder.Decode(&row) != nil || len(row) > maxResponseBytes {
			return nil, fmt.Errorf("installed evidence row is invalid or exceeds response bound")
		}
		result = append(result, row)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("installed row array is incomplete or has trailing data")
	}
	return result, nil
}

// InstalledPackCoverage returns a detached copy of the fixed finite claims.
func InstalledPackCoverage() map[string]string {
	return map[string]string{"scenario": "ordinary initial Flood audit; 5 positive, 4 negative, 3+3 isolated", "ownership": "operator-declared dedicated lab and continuous exclusion; not machine verified", "effects": "audit-only does not prevent ordinary notifications; controlled lab destinations required", "unproved": "authoritative dispatch, provider delivery, clear, recovery, other patterns, capacity", "activeAuditBytes": "original raw rows; not original whole-page envelopes", "historyBytes": "original complete page bytes"}
}
