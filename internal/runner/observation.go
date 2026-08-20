package runner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

type observationSnapshot struct {
	Sources       []oscar.HistoryRecord      `json:"sources"`
	Parents       []oscar.HistoryRecord      `json:"parents"`
	Audits        []oscar.AuditRecord        `json:"audits"`
	Notifications []oscar.NotificationRecord `json:"notifications,omitempty"`
	SourceReady   bool                       `json:"sourceReady"`
	AuditReady    bool                       `json:"auditReady"`
}

func (r *Runner) observeCase(ctx context.Context, run domain.Run, item compiler.CasePlan, started, injectedAt time.Time) (caseResult, error) {
	window := item.ObservationWindow
	if r.opts.ObservationWindow > 0 {
		window = r.opts.ObservationWindow
	}
	if window <= 0 {
		window = 35 * time.Second
	}
	deadline := injectedAt.Add(window)
	if injectedAt.IsZero() {
		deadline = r.opts.Now().Add(window)
	}
	fullWindow := requiresFullWindow(item.Assertions)
	var last observationSnapshot
	for {
		snapshot, err := r.readSnapshot(ctx, run, item, started, deadline)
		if err != nil {
			return caseResult{Name: item.Name, Code: item.Code}, err
		}
		last = snapshot
		assertions, err := evaluateAssertions(item.Assertions, snapshot)
		if err != nil {
			return caseResult{Name: item.Name, Code: item.Code}, err
		}
		now := r.opts.Now()
		atDeadline := !now.Before(deadline)
		if !fullWindow && snapshot.SourceReady && snapshot.AuditReady && assertionsPass(assertions) {
			if r.opts.Stabilization > 0 {
				if err := r.opts.Sleep(ctx, r.opts.Stabilization); err != nil {
					return caseResult{Name: item.Name, Code: item.Code}, err
				}
				stable, err := r.readSnapshot(ctx, run, item, started, deadline)
				if err != nil {
					return caseResult{Name: item.Name, Code: item.Code}, err
				}
				stableAssertions, evalErr := evaluateAssertions(item.Assertions, stable)
				if evalErr != nil {
					return caseResult{Name: item.Name, Code: item.Code}, evalErr
				}
				if assertionsPass(stableAssertions) {
					return buildCaseResult(item, stable, stableAssertions, domain.VerdictPass, "declared assertions remained satisfied through stabilization"), nil
				}
				last = stable
			}
		}
		if atDeadline {
			break
		}
		remaining := deadline.Sub(now)
		delay := r.opts.PollInterval
		if delay > remaining {
			delay = remaining
		}
		if err := r.opts.Sleep(ctx, delay); err != nil {
			return caseResult{Name: item.Name, Code: item.Code}, err
		}
	}
	assertions, err := evaluateAssertions(item.Assertions, last)
	if err != nil {
		return caseResult{Name: item.Name, Code: item.Code}, err
	}
	if !last.SourceReady || !last.AuditReady {
		return buildCaseResult(item, last, assertions, domain.VerdictInconclusive, "source history or correlation-audit eligibility was not proven by the deadline"), nil
	}
	if assertionsPass(assertions) {
		return buildCaseResult(item, last, assertions, domain.VerdictPass, "declared assertions matched the final bounded observation"), nil
	}
	return buildCaseResult(item, last, assertions, domain.VerdictFail, "one or more declared assertions differed from the final bounded observation"), nil
}

func (r *Runner) readSnapshot(ctx context.Context, run domain.Run, item compiler.CasePlan, started, deadline time.Time) (observationSnapshot, error) {
	expected := expectedSourceEvents(item)
	sourcesByFingerprint := map[string]oscar.HistoryRecord{}
	foundEvents := map[string]bool{}
	for alertName := range expected {
		records, err := r.api.FindHistory(ctx, oscar.HistoryQuery{AlertName: alertName, Start: started.Add(-time.Second), End: deadline.Add(time.Minute)})
		if err != nil {
			return observationSnapshot{}, err
		}
		for _, record := range records {
			if record.AlertName != alertName || record.Labels["oscar_test_run_id"] != run.ID || record.Fingerprint == "" {
				continue
			}
			eventID := record.Labels["oscar_test_event_id"]
			if !expected[alertName][eventID] {
				continue
			}
			foundEvents[alertName+"\x00"+eventID] = true
			sourcesByFingerprint[record.Fingerprint] = record
		}
	}
	snapshot := observationSnapshot{Sources: historyValues(sourcesByFingerprint)}
	expectedCount := 0
	for _, eventIDs := range expected {
		expectedCount += len(eventIDs)
	}
	snapshot.SourceReady = len(foundEvents) == expectedCount && len(sourcesByFingerprint) == expectedCount

	auditByID := map[int]oscar.AuditRecord{}
	auditedFingerprints := map[string]bool{}
	for _, source := range snapshot.Sources {
		audits, err := r.api.CorrelationAudit(ctx, source.Fingerprint)
		if err != nil {
			return observationSnapshot{}, err
		}
		for _, audit := range audits {
			if audit.AlertFingerprint != "" && audit.AlertFingerprint != source.Fingerprint {
				continue
			}
			if audit.RuleName != item.Rule.Name {
				continue
			}
			auditedFingerprints[source.Fingerprint] = true
			auditByID[audit.ID] = audit
		}
		if item.Rule.Pattern == "parent_child" {
			notifications, err := r.api.NotificationAudit(ctx, source.Fingerprint, started.Add(-time.Second), deadline.Add(time.Minute))
			if err != nil {
				return observationSnapshot{}, err
			}
			for _, notification := range notifications {
				if notification.AlertFingerprint == source.Fingerprint {
					snapshot.Notifications = append(snapshot.Notifications, notification)
				}
			}
		}
	}
	snapshot.Audits = auditValues(auditByID)
	snapshot.AuditReady = snapshot.SourceReady && len(auditedFingerprints) == len(snapshot.Sources)
	if item.Rule.EmitAlertName != "" {
		parents, err := r.api.FindHistory(ctx, oscar.HistoryQuery{AlertName: item.Rule.EmitAlertName, Start: started.Add(-time.Second), End: deadline.Add(time.Minute)})
		if err != nil {
			return observationSnapshot{}, err
		}
		parentsByFingerprint := map[string]oscar.HistoryRecord{}
		for _, parent := range parents {
			if parent.AlertName == item.Rule.EmitAlertName && parent.Labels["oscar_test_run_id"] == run.ID && parent.Fingerprint != "" {
				parentsByFingerprint[parent.Fingerprint] = parent
			}
		}
		snapshot.Parents = historyValues(parentsByFingerprint)
	}
	return snapshot, nil
}

func expectedSourceEvents(item compiler.CasePlan) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	for _, alert := range item.Alerts {
		eventID := alert.Labels["oscar_test_event_id"]
		if eventID == "" {
			continue
		}
		if result[alert.Name] == nil {
			result[alert.Name] = map[string]bool{}
		}
		result[alert.Name][eventID] = true
	}
	return result
}

func historyValues(input map[string]oscar.HistoryRecord) []oscar.HistoryRecord {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]oscar.HistoryRecord, 0, len(keys))
	for _, key := range keys {
		result = append(result, input[key])
	}
	return result
}

func auditValues(input map[int]oscar.AuditRecord) []oscar.AuditRecord {
	keys := make([]int, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	result := make([]oscar.AuditRecord, 0, len(keys))
	for _, key := range keys {
		result = append(result, input[key])
	}
	return result
}

func buildCaseResult(item compiler.CasePlan, snapshot observationSnapshot, assertions []assertionResult, verdict domain.Verdict, explanation string) caseResult {
	result := caseResult{Name: item.Name, Code: item.Code, Verdict: string(verdict), SyntheticCount: len(snapshot.Parents), ObservationComplete: true, Explanation: explanation, Assertions: assertions, SourceHistory: snapshot.Sources, SyntheticHistory: snapshot.Parents, Audits: snapshot.Audits, Notifications: snapshot.Notifications}
	for _, source := range snapshot.Sources {
		result.SourceFingerprints = append(result.SourceFingerprints, source.Fingerprint)
	}
	for _, audit := range snapshot.Audits {
		result.AuditOutcomes = append(result.AuditOutcomes, audit.Outcome)
	}
	for _, notification := range snapshot.Notifications {
		result.NotificationStatuses = append(result.NotificationStatuses, notification.Status)
	}
	result.Explanation = strings.TrimSpace(result.Explanation)
	if result.Explanation == "" {
		result.Explanation = fmt.Sprintf("case completed with %s", verdict)
	}
	return result
}
