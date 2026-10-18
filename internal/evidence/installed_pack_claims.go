package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func installedPlanDecode(raw []byte, plan *installedArchivePlan) error {
	if len(raw) > 128<<10 {
		return fmt.Errorf("installed plan exceeds bound")
	}
	if _, err := testpack.CanonicalJSON(raw); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return err
	}
	if len(keys) != 3 && len(keys) != 8 {
		return fmt.Errorf("installed plan is neither queued nor fully bound")
	}
	allowed := map[string]bool{"runId": true, "pattern": true, "inputPath": true, "kit": true, "installation": true, "apiTargetId": true, "receiverTargetId": true, "receiverBaseUrl": true}
	for k := range keys {
		if !allowed[k] {
			return fmt.Errorf("unknown installed plan field")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(plan)
}

func validateInstalledClaims(ctx context.Context, run domain.Run, plan installedArchivePlan, snapshot *storage.InstalledPackSnapshot, events []domain.RunEvent, index []installedArchiveIndex, observations map[string][]byte, kit *testpack.Kit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	noReport := len(run.CanonicalReportJSON) == 0 || bytes.Equal(bytes.TrimSpace(run.CanonicalReportJSON), []byte("null"))
	if noReport {
		if run.Status != domain.RunInterrupted || run.Verdict == domain.VerdictPass || run.Verdict == domain.VerdictFail {
			return fmt.Errorf("terminal claim has no original report")
		}
		return nil
	}
	var report oscar.InstalledPackRunReport
	if err := installedDecode(run.CanonicalReportJSON, &report); err != nil {
		return err
	}
	if report.APIVersion != "corrtest.oscar/installed-run-v1" || report.RunID != run.ID || report.InputPath != "installed-test-pack" || report.Verdict != run.Verdict || report.Cleanup != run.CleanupStatus || report.Failure != run.TerminalError || report.StartedAt.IsZero() || report.EndedAt.Before(report.StartedAt) || report.EndedAt.After(run.UpdatedAt) {
		return fmt.Errorf("installed report differs from original run")
	}
	if report.Verdict != domain.VerdictPass && report.Verdict != domain.VerdictFail && report.Verdict != domain.VerdictInconclusive {
		return fmt.Errorf("unsupported installed report verdict")
	}
	var capability map[string]string
	if err := json.Unmarshal(run.CapabilitySnapshotJSON, &capability); err != nil || !reflect.DeepEqual(capability, report.Coverage) || !reflect.DeepEqual(report.Coverage, oscar.InstalledPackCoverage()) {
		return fmt.Errorf("installed coverage claims differ")
	}
	// Report artifacts are exact successful publications; a failed publication may
	// leave an additional pending/available ledger row, never an invented success.
	available := map[string]domain.Artifact{}
	for _, item := range index {
		if item.Artifact.Availability == domain.ArtifactAvailable {
			available[item.Artifact.RelativePath] = item.Artifact
		}
	}
	reported := map[string]bool{}
	for _, a := range report.Artifacts {
		actual, ok := available[a.RelativePath]
		if !ok || reported[a.RelativePath] || a.SHA256 != actual.SHA256 || a.ByteSize != actual.ByteSize || a.MIMEType != actual.MIMEType {
			return fmt.Errorf("report artifact differs")
		}
		reported[a.RelativePath] = true
	}
	if report.Stage == "complete" && (len(reported) != len(index) || len(available) != len(index)) {
		return fmt.Errorf("complete run has incomplete artifacts")
	}
	if snapshot != nil {
		if report.Spec == nil || *report.Spec != snapshot.Registration.Spec || report.QualificationRun != snapshot.Registration.Spec.QualificationRun {
			return fmt.Errorf("report input binding differs")
		}
	} else {
		if len(report.Polls) != 0 || report.LastForwardAt != nil {
			return fmt.Errorf("unregistered report claims input")
		}
		// Registration may fail after the runner records its intended pure spec
		// in the report. Retain that intent only as failed, unattempted evidence.
		if report.Spec != nil {
			if report.Failure == "" || report.Verdict != domain.VerdictInconclusive || report.QualificationRun != report.Spec.QualificationRun || report.Spec.ManifestSHA256 != plan.Kit.ManifestSHA256 || report.Spec.ReceiverTargetID != plan.ReceiverTargetID || report.Spec.ReceiverBaseURL != plan.ReceiverBaseURL {
				return fmt.Errorf("unregistered input intent differs")
			}
			if _, err := oscar.FreezeInstalledPackInput(*report.Spec); err != nil {
				return err
			}
		}
	}
	if len(report.Polls) > 181 || len(report.Reads) > 3 {
		return fmt.Errorf("report observation count exceeds bound")
	}
	successfulReads := map[string]bool{}
	usedReadArtifacts := map[string]bool{}
	for n, read := range report.Reads {
		expectedKinds := []string{"configuration-before", "token-absence-full-datetime-range", "configuration-after"}
		if read.Kind != expectedKinds[n] || read.StartedAt.IsZero() || read.EndedAt.Before(read.StartedAt) || read.StartedAt.Before(report.StartedAt) || read.EndedAt.After(report.EndedAt) {
			return fmt.Errorf("report read lifetime differs")
		}
		for _, name := range read.Artifacts {
			if !reported[name] || usedReadArtifacts[name] {
				return fmt.Errorf("read artifact reference differs")
			}
			usedReadArtifacts[name] = true
		}
		prefix := "runs/" + run.ID + "/installed/preflight/"
		suffix := "before"
		if n == 1 {
			suffix = "absence"
		} else if n == 2 {
			suffix = "after"
		}
		prefix += suffix + "/"
		for _, name := range read.Artifacts {
			if !strings.HasPrefix(name, prefix) {
				return fmt.Errorf("read refers to foreign stage")
			}
		}
		if read.Error != "" {
			continue
		}
		if kit == nil {
			return fmt.Errorf("successful read lacks original kit")
		}
		if n == 1 {
			raw, pages, err := installedObservedSet(observations, read.Artifacts, prefix, false)
			if err != nil {
				return err
			}
			decoded, err := oscar.DecodeInstalledFloodEvidence(raw, pages, json.RawMessage("[]"))
			if err != nil {
				return err
			}
			if len(decoded.Active) != 0 {
				return fmt.Errorf("token absence contradicts Active")
			}
			for _, row := range decoded.History.Records {
				if row.Labels["qualification_run"] == report.QualificationRun || strings.HasPrefix(row.Labels["source_id"], report.QualificationRun+"-") {
					return fmt.Errorf("token absence contradicts History")
				}
			}
		} else {
			if len(read.Artifacts) != 6 {
				return fmt.Errorf("configuration read incomplete")
			}
			values := map[string]json.RawMessage{}
			for _, field := range []string{"packs", "load", "mapping", "rules", "rule", "runtime"} {
				name := prefix + field + ".json"
				if !containsInstalled(read.Artifacts, name) {
					return fmt.Errorf("configuration artifact missing")
				}
				values[field] = observations[name]
			}
			b, i := kit.Bindings(), plan.Installation
			expected := oscar.InstalledPackExpected{ManifestSHA256: b.ManifestSHA256, PackID: b.PackID, Version: b.Version, PackContentHash: b.PackContentHash, MappingNamespace: b.ProfileNamespace, MappingName: b.ProfileName, MappingSourceFile: b.MappingSourceFile, MappingStateSHA256: b.MappingStateSHA256, MappingRowKey: b.MappingRowKey, RuleJSON: kit.RuleJSON(), MappingStateJSON: kit.MappingStateJSON()}
			binding := oscar.InstalledPackBinding{TargetOrigin: i.TargetOrigin, ManifestSHA256: i.ManifestSHA256, LoadID: i.LoadID, RuleID: i.RuleID, Runtime: oscar.InstalledRuntimeBinding{LoadedAt: i.LoadedAt, LastAttemptMS: i.LastAttemptMS, LoadedRuleIDs: []int{i.RuleID}}}
			if err := oscar.ValidateInstalledPackObservation(expected, binding, oscar.InstalledPackObservation{Packs: values["packs"], Load: values["load"], Mapping: values["mapping"], Rules: values["rules"], Rule: values["rule"], Runtime: values["runtime"]}); err != nil {
				return err
			}
		}
		successfulReads[read.Kind] = true
	}
	if snapshot != nil && (!successfulReads["configuration-before"] || !successfulReads["token-absence-full-datetime-range"]) {
		return fmt.Errorf("recorded inputs lack successful original preflight")
	}
	var manifest oscar.InstalledPackManifest
	var lastForward *time.Time
	if snapshot != nil {
		var err error
		manifest, err = oscar.FreezeInstalledPackInput(snapshot.Registration.Spec)
		if err != nil {
			return err
		}
		if len(snapshot.Attempts) == 15 {
			all := true
			for _, a := range snapshot.Attempts {
				if a.Result == nil || a.Result.Class != oscar.InjectionForwarded || a.TransportError != "" {
					all = false
				}
			}
			if all {
				lastForward = snapshot.Attempts[14].CompletedAt
			}
		}
	}
	if report.LastForwardAt != nil && (lastForward == nil || !report.LastForwardAt.Equal(*lastForward)) {
		return fmt.Errorf("last forwarding time differs")
	}
	if len(report.Polls) > 0 && (report.LastForwardAt == nil || kit == nil) {
		return fmt.Errorf("observation lacks fifteen durable forwarding outcomes")
	}
	var observedEvents []domain.RunEvent
	for _, e := range events {
		if e.Type == "installed_pack.observed" {
			observedEvents = append(observedEvents, e)
		}
	}
	unmatchedFinal := report.Failure != "" && report.Verdict == domain.VerdictInconclusive && len(observedEvents)+1 == len(report.Polls)
	if len(observedEvents) != len(report.Polls) && !unmatchedFinal {
		return fmt.Errorf("poll event correspondence differs")
	}
	var adverse *oscar.InstalledFloodEvaluation
	var last oscar.InstalledFloodEvaluation
	var previousEnd time.Time
	var previousElapsed time.Duration
	for n, poll := range report.Polls {
		if err := ctx.Err(); err != nil {
			return err
		}
		if poll.Index != n || poll.StartedAt.IsZero() || poll.EndedAt.Before(poll.StartedAt) || poll.StartedAt.Before(*lastForward) || poll.EndedAt.After(report.EndedAt) || (!previousEnd.IsZero() && poll.StartedAt.Before(previousEnd)) || poll.ElapsedSinceLastForward < previousElapsed || poll.ElapsedSinceLastForward < 0 {
			return fmt.Errorf("poll timing/order differs")
		}
		prefix := fmt.Sprintf("runs/%s/installed/observations/%03d/", run.ID, n)
		raw, pages, err := installedObservedSet(observations, poll.Artifacts, prefix, true)
		if err != nil {
			return err
		}
		for _, name := range poll.Artifacts {
			if !reported[name] {
				return fmt.Errorf("poll lacks published artifact")
			}
		}
		decoded, err := oscar.DecodeInstalledFloodEvidence(raw, pages, observations[prefix+"audit-raw-rows.json"])
		if err != nil {
			return err
		}
		last = oscar.EvaluateInstalledFlood(manifest, oscar.Rule{ID: plan.Installation.RuleID, Name: plan.Kit.RuleName, Pattern: "flood"}, decoded)
		if last != poll.Evaluation {
			return fmt.Errorf("poll disposition differs from original raw evidence")
		}
		if last.Disposition == oscar.InstalledFloodViolation || last.Disposition == oscar.InstalledFloodAmbiguous {
			if adverse == nil || last.Disposition == oscar.InstalledFloodViolation {
				copy := last
				adverse = &copy
			}
		}
		if n < len(observedEvents) {
			var recorded oscar.InstalledPackPoll
			if err := installedDecode([]byte(observedEvents[n].DetailJSON), &recorded); err != nil {
				return err
			}
			if !reflect.DeepEqual(recorded, poll) || observedEvents[n].OccurredAt.Before(poll.EndedAt) {
				return fmt.Errorf("poll event differs")
			}
		}
		previousEnd = poll.EndedAt
		previousElapsed = poll.ElapsedSinceLastForward
	}
	if report.Evaluation != last || !reflect.DeepEqual(report.Adverse, adverse) {
		// A failed final HTTP/clock boundary can retain all raw responses and an
		// evaluation before the runner appends its poll. Recheck that one exact
		// additional set; it cannot establish a completed interval or event.
		if report.Failure == "" || report.Verdict != domain.VerdictInconclusive || snapshot == nil || lastForward == nil {
			return fmt.Errorf("report discarded prior adverse or changed final evaluation")
		}
		prefix := fmt.Sprintf("runs/%s/installed/observations/%03d/", run.ID, len(report.Polls))
		var names []string
		for name := range observations {
			if strings.HasPrefix(name, prefix) {
				if !reported[name] {
					return fmt.Errorf("unreported tail artifact")
				}
				names = append(names, name)
			}
		}
		raw, pages, err := installedObservedSet(observations, names, prefix, true)
		if err != nil {
			return err
		}
		decoded, err := oscar.DecodeInstalledFloodEvidence(raw, pages, observations[prefix+"audit-raw-rows.json"])
		if err != nil {
			return err
		}
		tail := oscar.EvaluateInstalledFlood(manifest, oscar.Rule{ID: plan.Installation.RuleID, Name: plan.Kit.RuleName, Pattern: "flood"}, decoded)
		if tail.Disposition == oscar.InstalledFloodViolation || tail.Disposition == oscar.InstalledFloodAmbiguous {
			if adverse == nil || tail.Disposition == oscar.InstalledFloodViolation {
				copy := tail
				adverse = &copy
			}
		}
		if report.Evaluation != tail || !reflect.DeepEqual(report.Adverse, adverse) {
			return fmt.Errorf("failed evaluation has no matching retained raw tail")
		}
	}
	if report.Failure != "" {
		if report.Verdict != domain.VerdictInconclusive {
			return fmt.Errorf("failed execution claims verdict")
		}
		return nil
	}
	if report.Stage != "complete" || report.Spec == nil || lastForward == nil || report.LastForwardAt == nil || len(report.Polls) == 0 || !successfulReads["configuration-after"] {
		return fmt.Errorf("successful report is incomplete")
	}
	final := report.Polls[len(report.Polls)-1]
	required := time.Duration(kit.Scenario().Budgets.WindowSeconds+kit.Scenario().Budgets.ObservationLagSeconds) * time.Second
	if final.EndedAt.Before(lastForward.Add(required)) || final.ElapsedSinceLastForward < required || final.ElapsedSinceLastForward >= time.Duration(kit.Scenario().Budgets.ExecutionSeconds)*time.Second || final.EndedAt.Sub(report.StartedAt) >= time.Duration(kit.Scenario().Budgets.ExecutionSeconds)*time.Second {
		return fmt.Errorf("complete observation interval missing")
	}
	expected := domain.VerdictInconclusive
	if adverse != nil {
		last = *adverse
	}
	if last.Disposition == oscar.InstalledFloodEligible {
		expected = domain.VerdictPass
	} else if last.Disposition == oscar.InstalledFloodViolation {
		expected = domain.VerdictFail
	}
	if report.Verdict != expected {
		return fmt.Errorf("report verdict contradicts retained observations")
	}
	return nil
}
func containsInstalled(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func installedObservedSet(blobs map[string][]byte, names []string, prefix string, audit bool) (json.RawMessage, []json.RawMessage, error) {
	if len(names) < 2 || len(names) > 102 {
		return nil, nil, fmt.Errorf("observation artifact count")
	}
	seen := map[string]bool{}
	var historyNames []string
	for _, name := range names {
		if seen[name] || blobs[name] == nil || !strings.HasPrefix(name, prefix) {
			return nil, nil, fmt.Errorf("observation artifact correspondence")
		}
		seen[name] = true
		suffix := strings.TrimPrefix(name, prefix)
		switch {
		case suffix == "active-raw-rows.json":
		case suffix == "audit-raw-rows.json" && audit:
		case strings.HasPrefix(suffix, "history-page-"):
			historyNames = append(historyNames, name)
		default:
			return nil, nil, fmt.Errorf("unknown observation artifact")
		}
	}
	if !seen[prefix+"active-raw-rows.json"] || (audit && !seen[prefix+"audit-raw-rows.json"]) || len(historyNames) == 0 {
		return nil, nil, fmt.Errorf("partial observation")
	}
	sort.Strings(historyNames)
	var pages []json.RawMessage
	for n, name := range historyNames {
		if name != fmt.Sprintf("%shistory-page-%03d.json", prefix, n+1) {
			return nil, nil, fmt.Errorf("noncontiguous History pages")
		}
		pages = append(pages, blobs[name])
	}
	return blobs[prefix+"active-raw-rows.json"], pages, nil
}
