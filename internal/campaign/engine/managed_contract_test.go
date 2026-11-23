package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestCommissionedDraftsHaveMissingSafeScopedPriorities(t *testing.T) {
	h := newHarness(t)
	for _, s := range []scenario.Scenario{scenario.AlertingDayV3(), scenario.AlertingDaySmokeV3()} {
		r := &run{e: New(h.deps, h.clock), s: s, id: h.id, sinkID: "sink", notifiers: map[string]string{"CAMPAIGN_SINK": "sink", "ALERTHISTORY": "history"}}
		h.oscar.policies = map[string]*fakePolicy{}
		ctx := context.Background()
		if err := r.createDrafts(ctx); err != nil {
			t.Fatal(err)
		}
		m := managedLane{run: r, managedDeps: &managedDeps{config: managed.Config{SourceID: "source", TID: "ne"}}}
		edit := *s.NativeWorkload.ManagedPolicy
		edit.Key = "EDIT"
		for _, p := range []scenario.PolicySpec{*s.NativeWorkload.ManagedPolicy, *s.NativeWorkload.ManagedRepeat, edit} {
			if err := m.activate(ctx, p); err != nil {
				t.Fatal(err)
			}
		}
		ordinary := map[string]string{"campaign_run": r.id, "campaign_lane": laneFor(s.Resend[0])}
		managedLabels := map[string]string{"source_id": "source", "node": "ne", "condition": m.condition("POL")}
		checked := map[string]bool{}
		var ordinaryHit, managedHit int
		for _, row := range h.oscar.policies {
			def := row.def
			key := def.NotifierID + def.Action + strconv.Itoa(def.Priority)
			if checked[key] {
				t.Fatal("catalog priority conflict", def)
			}
			checked[key] = true
			match := func(labels map[string]string) bool {
				for _, clause := range strings.Split(def.Condition, " and ") {
					pair := regexp.MustCompile(`^([a-z_]+) == ("[^"]+")$`).FindStringSubmatch(clause)
					if pair == nil {
						t.Fatalf("outside product missing-safe grammar: %s", def.Condition)
					}
					v, err := strconv.Unquote(pair[2])
					if err != nil {
						t.Fatal(err)
					}
					if labels[pair[1]] != v {
						return false
					}
				}
				return true
			}
			if match(ordinary) {
				ordinaryHit++
				if def.Priority != 1 {
					t.Fatal(def)
				}
			}
			if match(managedLabels) {
				managedHit++
				if def.Priority != 5 {
					t.Fatal(def)
				}
			}
		}
		if len(checked) != len(s.Resend)+3 || ordinaryHit != 1 || managedHit != 1 {
			t.Fatal(checked, ordinaryHit, managedHit)
		}
	}
}
func TestEffectiveMemberKeepsSourceIdentity(t *testing.T) {
	now := time.Unix(100, 0)
	s := managed.SourceReceipt{SourceSeverity: "major", Starts: now.Format(time.RFC3339Nano), Fingerprint: "source", SourceID: "src", TID: "ne", Correlation: "c"}
	v := lane.NativeMember{Fingerprint: "source", StartsAt: now, EndsAt: now.Add(time.Minute), Labels: map[string]string{"source_id": "src", "node": "ne", "severity": "critical", "correlation_key": "c"}}
	if !observedMember(v, s, "critical", now) || observedMember(v, s, "major", now) {
		t.Fatal("source and effective severity confused")
	}
	v.StartsAt = v.StartsAt.Add(time.Millisecond)
	if observedMember(v, s, "critical", now) {
		t.Fatal("wrong start admitted")
	}
}

func TestTimerChecksKindPinnedScopeAndDispatchBeforeCallback(t *testing.T) {
	base := time.Unix(10000, 0).UTC()
	p := &policyState{id: "rule", notifierID: "sink", rev: 5, spec: *scenario.AlertingDayV3().NativeWorkload.ManagedPolicy}
	b := managed.Binding{ID: "binding", Rule: "rule", Revision: "immutable", CatalogRevision: 5, Notifier: "sink", Action: "notification", State: "bound"}
	if _, err := ownedBinding(p, []managed.Binding{b}); err != nil {
		t.Fatal(err)
	}
	altered := b
	altered.CatalogRevision++
	if _, err := ownedBinding(p, []managed.Binding{altered}); err == nil {
		t.Fatal("wrong catalog revision")
	}
	for _, tc := range []struct {
		name  string
		alter func(*managed.Effect, *dispatchWindow)
	}{
		{"correct", func(*managed.Effect, *dispatchWindow) {}}, {"swapped-slot", func(e *managed.Effect, _ *dispatchWindow) { e.Kind = "escalate"; e.Severity = "critical" }},
		{"foreign-revision", func(e *managed.Effect, _ *dispatchWindow) { e.Revision = "other" }},
		{"foreign-binding", func(e *managed.Effect, _ *dispatchWindow) { e.BindingID = "other" }},
		{"foreign-receiver", func(e *managed.Effect, _ *dispatchWindow) { e.Receiver = "other" }},
		{"foreign-target", func(e *managed.Effect, _ *dispatchWindow) { e.ScopeNotifier = "other" }},
		{"early-dispatch-late-callback", func(_ *managed.Effect, w *dispatchWindow) {
			w.Before = w.Before.Add(-3 * time.Second)
			w.By = w.By.Add(-3 * time.Second)
		}},
		{"uncertain-first-poll", func(_ *managed.Effect, w *dispatchWindow) { w.Before = time.Time{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := slotClock{active: base, last: base.Add(time.Minute), source: "major", receiver: "canonical"}
			due := base.Add(2 * time.Minute)
			w := dispatchWindow{due, due.Add(time.Second)}
			e := managed.Effect{Kind: "resend", Operation: "op", ChildState: "sent", Severity: "major", Receiver: "canonical", Target: "digest", ScopeNotifier: "sink", ScopeAction: "notification", TargetState: "confirmed", Callback: "cb", Child: "C", ScheduleRoot: "root", Logical: "root", BindingID: "binding", Revision: "immutable", Notifier: "sink", Action: "notification", ReceivedAt: due.Add(3 * time.Second).Format(time.RFC3339Nano), ConfirmedAt: due.Add(8 * time.Second).Format(time.RFC3339Nano)}
			tc.alter(&e, &w)
			if err := clock.accept(p, b, e, w); (err == nil) != (tc.name == "correct") {
				t.Fatal(err)
			}
		})
	}
}
func TestBulkChecksSelectedAndUnselectedPhysicalPayloads(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	f := NativeEpisodeFixture{History: observedHistory{ID: "h", Labels: map[string]string{"aid": "A"}}, Source: managed.SourceReceipt{Starts: start.Format(time.RFC3339Nano), SourceID: "s", TID: "ne", Correlation: "c"}}
	sibling := NativeEpisodeFixture{History: observedHistory{Labels: map[string]string{"aid": "B"}}}
	e := managed.Effect{Kind: "resend", Logical: "root", Operation: "original", Child: "C", Callback: "cb", Target: "target", TargetState: "confirmed", Notifier: "sink", Action: "notification", Severity: "major", EnteredAt: start.Format(time.RFC3339Nano), ConfirmedAt: start.Add(time.Second).Format(time.RFC3339Nano)}
	receipt := targetops.Receipt{ID: 1, ReceivedAt: start.Add(time.Millisecond), Alerts: []targetops.SinkAlert{{HistoryID: "h", StartsAt: f.Source.Starts, Status: "firing", Labels: map[string]string{"aid": "A", "node": "ne", "source_id": "s", "severity": "major", "correlation_key": "c"}}}}
	check := func(mine, other []targetops.Receipt) bool {
		ok, err := bulkIsolation(f, sibling, "root", "sink", "C", []managed.Effect{e}, mine, other)
		return ok && err == nil
	}
	if !check([]targetops.Receipt{receipt}, nil) {
		t.Fatal("selected physical payload lost")
	}
	other := targetops.Receipt{ID: 1, Alerts: []targetops.SinkAlert{{Status: "firing", Labels: map[string]string{"aid": "B"}}}}
	if check([]targetops.Receipt{receipt}, []targetops.Receipt{other}) {
		t.Fatal("group leaked sibling under selected root")
	}
	receipt.Alerts[0].StartsAt = start.Add(time.Millisecond).Format(time.RFC3339Nano)
	if check([]targetops.Receipt{receipt}, nil) {
		t.Fatal("replacement episode payload")
	}
	if ok, _ := matchOriginalSink([]managed.Effect{e}, []targetops.Receipt{receipt}, "sink", "c", "C", f); ok {
		t.Fatal("manual original child accepted another episode's payload")
	}
}

type lifecycleTrace struct {
	fixture NativeEpisodeFixture
	steps   []string
	fail    string
}

func (l *lifecycleTrace) step(s string) error {
	l.steps = append(l.steps, s)
	if s == l.fail {
		return errors.New("original unresolved")
	}
	return nil
}
func (l *lifecycleTrace) raiseOriginal(context.Context) (NativeEpisodeFixture, error) {
	return l.fixture, l.step("source")
}
func (l *lifecycleTrace) timedAndResolve(_ context.Context, f NativeEpisodeFixture) error {
	if !reflect.DeepEqual(f, l.fixture) {
		return errors.New("foreign source")
	}
	return l.step("timers_ack_clear")
}
func (l *lifecycleTrace) refireBulk(_ context.Context, f NativeEpisodeFixture) error {
	if !reflect.DeepEqual(f, l.fixture) {
		return errors.New("foreign original")
	}
	return l.step("refire_bulk")
}
func (l *lifecycleTrace) ticketSubjects(context.Context) error { return l.step("tickets") }
func TestManagedLifecycleStopsAtOriginalUnsettledStage(t *testing.T) {
	stages := []string{"source", "timers_ack_clear", "refire_bulk", "tickets"}
	for i, stop := range append(append([]string{}, stages...), "") {
		log := lifecycleTrace{fixture: NativeEpisodeFixture{Source: managed.SourceReceipt{Authority: "authority", Episode: "original"}, History: observedHistory{ID: "selected", EpisodeNumber: 2}}, fail: stop}
		err := exerciseManagedSubjects(context.Background(), &log)
		if (err != nil) != (stop != "") || !reflect.DeepEqual(log.steps, stages[:min(i+1, len(stages))]) {
			t.Fatal(stop, log.steps, err)
		}
	}
}
func TestOriginalUnknownSurvivesClosedSourceAndCleanupRerun(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := &run{id: h.id, e: New(h.deps, h.clock), s: scenario.ManagedAlertsSmokeV1()}
	m := &managedLane{run: r}
	f := NativeEpisodeFixture{History: observedHistory{ID: "history"}}
	s := managed.Snapshot{Effects: []managed.Effect{{Operation: "original", Target: "target", Child: "child", ChildState: "unknown_requires_reconciliation"}}}
	if err := m.holdUnknown(ctx, f, s); err == nil {
		t.Fatal("unresolved owner ignored")
	}
	for _, event := range []string{"native.tl1.started", "native.tl1.closed"} {
		if err := m.save(ctx, event, map[string]bool{"verified": true}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if st := r.cleanup(ctx); st != domain.CleanupDirty {
			t.Fatal("lost original obligation", st)
		}
	}
	open, err := h.db.OpenMutations(ctx, h.id)
	if err != nil || len(open) != 1 || open[0].ObjectID != "original/target/child" {
		t.Fatal(open, err)
	}

}

type originalReaderFixture struct {
	rows  managed.OriginalReceipt
	reads int
}

func (f *originalReaderFixture) Original(_ context.Context, history, op string) (managed.OriginalReceipt, error) {
	f.reads++
	if history != "history" || op != "original" {
		return managed.OriginalReceipt{}, errors.New("foreign owner read")
	}
	return f.rows, nil
}
func TestMissingActorRetainsManualAndSettlesExactSQLChild(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	one := managed.OriginalChild{History: "history", Episode: 3, Root: "original", Operation: "original", Kind: "resend", OpState: "confirmed", Target: "target", TargetState: "confirmed", ManifestOwned: 1, Expected: 2, Reported: 2, Aggregate: "partial", Callback: "callback", Name: "sink", Action: "notification", Notifier: "sink-id", PolicyReceipt: "physical-sink", Child: "physical-sink", ReceiptType: "NotificationDelivery", State: "sent", ProviderAction: "notification", ProviderOutcome: "sent", ProviderConfirmed: "2026-11-22T00:00:01Z"}
	two := one
	two.Name, two.Notifier, two.Action, two.Child, two.PolicyReceipt, two.State, two.ProviderOutcome, two.ProviderConfirmed = "ALERTHISTORY", "history-id", "ticket", "physical-ticket", "physical-ticket", "suppressed", "suppressed", ""
	two.ProviderAction = "ticket"
	reader := &originalReaderFixture{rows: managed.OriginalReceipt{Children: []managed.OriginalChild{one, two}}}
	h.deps.managed = &managedDeps{setupErr: errors.New("operator session unavailable"), originals: reader}
	r := &run{id: h.id, e: New(h.deps, h.clock), s: scenario.ManagedAlertsSmokeV1()}
	owner := originalOwner{Kind: "effect", History: "history", Episode: 3, Root: "original", Operation: "original", Target: "target", Child: "physical-sink"}
	if !terminalEffect(owner, reader.rows) {
		t.Fatal("original sent child in settled partial unrecoverable")
	}
	owner.Child = "replacement"
	if terminalEffect(owner, reader.rows) {
		t.Fatal("foreign child settled original")
	}
	owner.Child = "physical-sink"
	if _, err := r.record(ctx, kindManagedOwner, "sql-child", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := r.record(ctx, kindManagedOwner, "request", originalOwner{Kind: "manual_resend", History: "history", Episode: 3, Request: "request"}); err != nil {
		t.Fatal(err)
	}
	if got := r.cleanup(ctx); got != domain.CleanupDirty {
		t.Fatal(got)
	}
	open, err := h.db.OpenMutations(ctx, h.id)
	if err != nil || len(open) != 1 || open[0].ObjectID != "request" || reader.reads != 1 {
		t.Fatal(open, err, reader.reads)
	}
	if err := r.recoverOriginal(ctx, open[0]); err == nil || !strings.Contains(err.Error(), "signed resend status reader unavailable") {
		t.Fatal(err)
	}
}
func TestManagedEventExportOmitsSignedHistoryAndArbitraryLabels(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m := managedLane{run: &run{id: h.id, e: New(h.deps, h.clock)}}
	secret := "sentinel-must-never-leave"
	labels := map[string]string{"aid": "owned", "arbitrary": secret}
	row := oscar.HistoryRecord{ID: "selected", Labels: labels, Annotations: map[string]string{"alarm_key": "keep-original-key", "oscar_managed_history": "delivery." + secret, "arbitrary": secret}}
	f := NativeEpisodeFixture{History: historyEvidence(row)}
	member := lane.NativeMember{Labels: labels}
	rs := []targetops.Receipt{{ID: 9, Alerts: []targetops.SinkAlert{{Labels: labels}}}}
	values := map[string]any{"native.source_observed": f, "native.resolved": map[string]any{"rows": []oscar.HistoryRecord{row}}, "native.managed.segment": map[string]any{"fixture": f, "receipts": rs}, "native.managed.refresh": map[string]any{"member": member}, "native.managed.effects": map[string]any{"sink": rs}, "native.bulk.isolation": map[string]any{"selected": f, "sibling": f, "selectedReceipts": rs}, "native.suppression_changed": map[string]any{"subject": f}, "native.ordinary.observed": map[string]any{"member": member}}
	for k, v := range values {
		if err := m.save(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	atts, _, err := m.run.e.exportEvents(ctx, h.id, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	for _, a := range atts {
		z, err := gzip.NewReader(bytes.NewReader(a.Content))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(&body, z); err != nil {
			t.Fatal(err)
		}
		z.Close()
	}
	for k := range values {
		if !strings.Contains(body.String(), k) {
			t.Fatal("missing event", k)
		}
	}
	if strings.Contains(body.String(), secret) || strings.Contains(body.String(), "oscar_managed_history") || strings.Contains(body.String(), "arbitrary") || !strings.Contains(body.String(), "keep-original-key") {
		t.Fatal("bundle missed allowlist boundary")
	}
}

func TestLabelDrivenTicketRequiresOriginalWorkflowAndAudit(t *testing.T) {
	f := NativeEpisodeFixture{History: observedHistory{ID: "h", EpisodeNumber: 2, Labels: map[string]string{"create_ticket": "snow"}}}
	row := managed.TicketProof{Flow: "flow", History: "h", Episode: 2, Incident: "INC1", Operation: "op", Audit: "op", Origin: "automatic", System: "snow", AuditSystem: "snow", AuditStatus: "success", AuditIncident: "INC1"}
	for _, edit := range []func(*managed.TicketProof){nil, func(r *managed.TicketProof) { r.History = "other" }, func(r *managed.TicketProof) { r.Episode = 1 }, func(r *managed.TicketProof) { r.Audit = "foreign" }, func(r *managed.TicketProof) { r.Origin = "adopted" }, func(r *managed.TicketProof) { r.AuditIncident = "other" }} {
		r := row
		if edit != nil {
			edit(&r)
		}
		proof, err := automaticTicket(f, []managed.TicketProof{r}, "INC1")
		if (err == nil && proof.Flow == "flow") != (edit == nil) {
			t.Fatal(r, proof, err)
		}
	}
}

func TestCrossRouteEscalationRequiresOriginalMajorWriteAndCriticalB(t *testing.T) {
	zero := 0
	f := NativeEpisodeFixture{History: observedHistory{ID: "h"}, Source: managed.SourceReceipt{Episode: "ep", Authority: "am", Generation: 1, Variant: 2, Receiver: "A"}}
	b := managed.Binding{ID: "p", Revision: "p1", Notifier: "old-A", Action: "notification"}
	e := managed.Effect{Kind: "escalate", Receiver: "B", ScopeSize: &zero, CommandScopeSize: &zero, Target: "target", TargetState: "confirmed", Operation: "op",
		Callback: "callback", Child: "child", ConfirmedAt: "present", ChildState: "sent", SourceWrite: "write", Logical: "op", ScheduleRoot: "op", BindingID: "p", Revision: "p1", Severity: "critical", Variant: 2, Notifier: "controlled-B", Action: "notification"}
	s := managed.Snapshot{Effects: []managed.Effect{e}, Dispatches: []managed.Dispatch{{Root: "op", Attempt: "op", State: "confirmed", Kind: "escalate"}},
		Source: []managed.SourceReceipt{{History: "h", Receiver: "A", WriteID: "write", Episode: "ep", Authority: "am", Generation: 1, Variant: 2, SourceSeverity: "major", Floor: "critical"}}}
	hit, err := crossRouteEffect(f, s, b, "B", "controlled-B")
	if err != nil || hit.Child != "child" {
		t.Fatal("Critical receiver/child unproved", err)
	}
	for _, alter := range []func(*managed.Effect){
		func(e *managed.Effect) { e.Receiver = "A" }, func(e *managed.Effect) { e.SourceWrite = "other" },
		func(e *managed.Effect) { one := 1; e.CommandScopeSize = &one }, func(e *managed.Effect) { e.ChildState = "unknown_requires_reconciliation" },
	} {
		s.Effects[0] = e
		alter(&s.Effects[0])
		hit, err = crossRouteEffect(f, s, b, "B", "controlled-B")
		if err == nil && hit.Child != "" {
			t.Fatal("invalid cross-route evidence accepted")
		}
	}
}
