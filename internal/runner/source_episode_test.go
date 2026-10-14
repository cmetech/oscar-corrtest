package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

type episodeHistoryAPI struct {
	*checkpointAPI
	mode    string
	audits  int
	reverse bool
}

func (api *episodeHistoryAPI) FindHistory(ctx context.Context, q oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	rows, err := api.checkpointAPI.FindHistory(ctx, q)
	if err != nil {
		return nil, err
	}
	current := map[string]compiler.AlertPlan{}
	for _, sent := range api.sent {
		current[sent.Labels["oscar_test_event_id"]] = sent
	}
	result := map[string]oscar.HistoryRecord{}
	for _, row := range rows {
		sent, found := current[row.Labels["oscar_test_event_id"]]
		if !found {
			continue
		}
		row.StartsAt = sent.StartsAt
		row.Status = sent.Status
		result[row.Fingerprint] = row
	}
	var out []oscar.HistoryRecord
	for _, row := range result {
		switch api.mode {
		case "missing empty label":
			row.Labels = cloneTestLabels(row.Labels)
			delete(row.Labels, "empty_identity")
		case "terminal transient":
			row.Status = "firing"
			if len(api.sent) == 11 && api.Now().Equal(api.sentAt[10]) {
				row.Status = "resolved"
			}
		case "refire stale":
			if len(api.sent) >= 7 {
				row.StartsAt = row.StartsAt.Add(-time.Millisecond)
			}
		case "initial collision":
			if len(api.sent) == 5 && api.Now().Equal(api.sentAt[4]) {
				row.Fingerprint = "same-fingerprint"
			}
		case "initial missing":
			if len(api.sent) == 5 && api.Now().Equal(api.sentAt[4]) {
				row.StartsAt = time.Time{}
			}
		case "missing":
			row.StartsAt = time.Time{}
		case "stale":
			row.StartsAt = row.StartsAt.Add(-time.Second)
		case "future":
			row.StartsAt = row.StartsAt.Add(time.Second)
		case "changed labels":
			row.Labels = cloneTestLabels(row.Labels)
			row.Labels["site"] = "another"
		case "unknown status":
			row.Status = "unknown"
		}
		out = append(out, row)
		if api.mode == "old competing" || api.mode == "missing competing" || api.mode == "future competing" || api.mode == "duplicate current" {
			other := row
			switch api.mode {
			case "missing empty label":
				row.Labels = cloneTestLabels(row.Labels)
				delete(row.Labels, "empty_identity")
			case "terminal transient":
				row.Status = "firing"
				if len(api.sent) == 11 && api.Now().Equal(api.sentAt[10]) {
					row.Status = "resolved"
				}
			case "old competing":
				other.StartsAt = other.StartsAt.Add(-time.Second)
				other.Status = "resolved"
			case "missing competing":
				other.StartsAt = time.Time{}
			case "future competing":
				other.StartsAt = other.StartsAt.Add(time.Second)
			}
			out = append(out, other)
		}
		if api.mode == "conflicting" {
			other := row
			other.Status = "resolved"
			if row.Status == "resolved" {
				other.Status = "firing"
			}
			out = append(out, other)
		}
	}
	if api.reverse {
		slices.Reverse(out)
	}
	return out, nil
}
func cloneTestLabels(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (api *episodeHistoryAPI) CorrelationAudit(ctx context.Context, key string) ([]oscar.AuditRecord, error) {
	api.audits++
	return api.checkpointAPI.CorrelationAudit(ctx, key)
}
func episodeScenario() scenario.Scenario {
	doc := stagedFlood()
	doc.Cases[0].Checkpoints = []scenario.Checkpoint{{Name: "five-current-firing", AfterEvent: 5, Window: time.Second, Assertions: []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "firing", Equals: 5}}}}
	doc.Cases[0].Events = append(doc.Cases[0].Events, scenario.Event{Role: "interface_down", Source: "child_0", Status: "resolved", Delay: 8 * time.Second}, scenario.Event{Role: "interface_down", Source: "child_0", Status: "firing", Delay: 10 * time.Second})
	doc.Cases[0].Checkpoints = append(doc.Cases[0].Checkpoints, scenario.Checkpoint{Name: "source-recovered", AfterEvent: 6, Window: time.Second, Assertions: []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: 1}, {Kind: "source-episode-state-count", Outcome: "firing", Equals: 4}}}, scenario.Checkpoint{Name: "source-refired", AfterEvent: 7, Window: time.Second, Assertions: []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "firing", Equals: 5}}})
	doc.Cases[0].Assertions = []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "firing", Equals: 5}}
	return doc
}
func TestNamedSourceEpisodeHistoryCountsCurrentRecoveryAndRefire(t *testing.T) {
	db := openDatabase(t)
	run := newRun()
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, episodeScenario(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	api := &episodeHistoryAPI{checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}}
	if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.GetRun(context.Background(), run.ID)
	var report struct {
		Checkpoints []struct {
			Verdict    string
			Assertions []struct {
				Verdict  string
				Observed int
			}
		}
	}
	json.Unmarshal(stored.CanonicalReportJSON, &report)
	if stored.Verdict != domain.VerdictPass || len(report.Checkpoints) != 3 {
		t.Fatalf("episode evidence incomplete: %s", stored.CanonicalReportJSON)
	}
	for _, point := range report.Checkpoints {
		if point.Verdict != "PASS" {
			t.Fatalf("checkpoint did not pass: %+v", point)
		}
	}
	// Only N01's existing audit assertions need the correlation endpoint.
	if api.audits != 4 {
		t.Fatalf("source visibility borrowed audit evidence: queries=%d", api.audits)
	}
}
func TestNamedSourceEpisodeHistoryCannotUseUnprovedRows(t *testing.T) {
	for _, mode := range []string{"missing", "stale", "future", "changed labels", "unknown status", "conflicting"} {
		t.Run(mode, func(t *testing.T) {
			db := openDatabase(t)
			run := newRun()
			if err := db.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, episodeScenario(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			api := &episodeHistoryAPI{mode: mode, checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}}
			if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
				t.Fatal(err)
			}
			stored, _ := db.GetRun(context.Background(), run.ID)
			if stored.Verdict != domain.VerdictInconclusive || len(api.sent) != 5 {
				t.Fatalf("%s evidence gave verdict=%s sent=%d", mode, stored.Verdict, len(api.sent))
			}
		})
	}
}
func TestSourceEpisodeAssertionRejectsUnnamedOrUnknownOutcome(t *testing.T) {
	for _, bad := range []string{"unnamed", "outcome"} {
		t.Run(bad, func(t *testing.T) {
			doc := episodeScenario()
			if doc.Cases[0].Labels == nil {
				doc.Cases[0].Labels = map[string]string{}
			}
			doc.Cases[0].Labels["empty_identity"] = ""
			if bad == "unnamed" {
				doc.Cases[0].Events[1].Source = ""
			} else {
				doc.Cases[0].Assertions[0].Outcome = "physically_cleared"
			}
			if err := scenario.Validate(doc); err == nil {
				t.Fatal(fmt.Sprintf("invalid %s assertion accepted", bad))
			}
		})
	}
}

func TestSourceEpisodeHistoryRowOrderAndRefireBoundary(t *testing.T) {
	for _, mode := range []string{"old competing", "duplicate current", "missing competing", "future competing", "conflicting", "refire stale", "initial missing", "initial collision", "missing empty label", "present empty label"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", mode, reverse), func(t *testing.T) {
				db := openDatabase(t)
				run := newRun()
				if err := db.CreateRun(context.Background(), run); err != nil {
					t.Fatal(err)
				}
				doc := episodeScenario()
				if doc.Cases[0].Labels == nil {
					doc.Cases[0].Labels = map[string]string{}
				}
				doc.Cases[0].Labels["empty_identity"] = ""
				if mode == "initial missing" || mode == "initial collision" {
					doc.Cases[0].Checkpoints[0].Assertions = []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: 0}}
				}
				plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
				if err != nil {
					t.Fatal(err)
				}
				api := &episodeHistoryAPI{mode: mode, reverse: reverse, checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 123456789, time.UTC))}}
				if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
					t.Fatal(err)
				}
				stored, _ := db.GetRun(context.Background(), run.ID)
				want := domain.VerdictInconclusive
				if mode == "old competing" || mode == "duplicate current" || mode == "present empty label" {
					want = domain.VerdictPass
				}
				if stored.Verdict != want {
					t.Fatalf("verdict=%s want=%s report=%s", stored.Verdict, want, stored.CanonicalReportJSON)
				}
				if mode == "refire stale" && len(api.sent) != 7 {
					t.Fatalf("refire was not boundary: sent=%d", len(api.sent))
				}
			})
		}
	}
}

func TestTerminalSourceEpisodeZeroRetainsObservedViolation(t *testing.T) {
	db := openDatabase(t)
	run := newRun()
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	doc := episodeScenario()
	doc.Cases[0].Checkpoints = nil
	doc.Cases[0].Assertions = []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: 0}}
	plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	api := &episodeHistoryAPI{mode: "terminal transient", checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}}
	if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, ObservationWindow: 30 * time.Second}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.GetRun(context.Background(), run.ID)
	if stored.Verdict != domain.VerdictFail {
		t.Fatalf("transient terminal violation verdict=%s", stored.Verdict)
	}
}
