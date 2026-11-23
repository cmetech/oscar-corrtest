package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestAbsentTailAndSyntheticTicketSurviveAnotherFailure(t *testing.T) {
	s := scenario.AlertingDayV3()
	r := &run{id: "fixture", s: s, t0: time.Unix(1700000000, 0)}
	v := newNativeTally(r)
	for i := 0; i < 14; i++ {
		for _, lane := range []string{"POL", "REPEAT"} {
			for _, k := range []string{"native.managed.segment", "native.managed.segment_closed"} {
				v.segments[fmt.Sprintf("%s/%s/%d", k, lane, i)] = true
			}
		}
	}
	v.finish()
	card := score.Scorecard{Verdict: domain.VerdictPass}
	judge(&card, []string{"observed duplicate"}, v.Missing)
	reasons := strings.Join(card.Reasons, " ")
	if !card.EvidenceIncomplete || card.Verdict != domain.VerdictFail || !strings.Contains(reasons, "native.managed.segment_closed/POL/14") || !strings.Contains(reasons, "native.synthetic_ticket") {
		t.Fatal(card)
	}
}
func TestGroupedProviderTotalsUsePhysicalAndMemberDenominators(t *testing.T) {
	v := newNativeTally(&run{s: scenario.ManagedAlertsSmokeV1()})
	group := []targetops.Receipt{{ID: 19, Alerts: []targetops.SinkAlert{{AlertName: "A"}, {AlertName: "B"}}}}
	v.receipts(group, "A")
	v.receipts(group, "B")
	v.receipts(group, "A")
	v.finish()
	if v.PhysicalRequests != 1 || v.ProviderMemberReceipts != 2 {
		t.Fatal(v.nativeEvidence)
	}
}
func TestSmokeCannotPassOnLifecycleMarker(t *testing.T) {
	v := newNativeTally(&run{id: "fixture", s: scenario.ManagedAlertsSmokeV1()})
	v.consume(domain.CampaignEvent{Type: "native.managed.functional", Detail: mustJSON(map[string]bool{"completed": true})})
	v.finish()
	if len(v.Missing) == 0 || v.Facets["native.managed.functional"] != 1 {
		t.Fatal(v.nativeEvidence)
	}
}

func TestUncommissionedOrPartialTraceCannotScore(t *testing.T) {
	s := scenario.AlertingDayControlV1()
	r := &run{id: "camp_cost", s: s, t0: time.Unix(1700000000, 0), e: &Engine{d: Deps{ObserverDir: t.TempDir()}}}
	r.opts.Provenance = &domain.CampaignProvenance{TargetBuild: "oscar", CorrTestBuild: "corrtest", WorkloadConfig: "work"}
	raw := costFile{Run: r.id, Hash: s.Hash(), TargetBuild: "oscar", CorrTestBuild: "corrtest", WorkloadConfig: "work", ObserverConfig: strings.Repeat("b", 64), From: r.t0, Through: r.t0.Add(s.LoadEnd)}
	half, cpu := 0.5, 1.
	raw.SamplingFraction = &half
	raw.ObserverCPUSeconds = &cpu
	if err := os.WriteFile(filepath.Join(r.e.d.ObserverDir, r.id+".json"), mustJSON(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.costCapture(newNativeTally(r), nil, r.t0.Add(s.LoadEnd)); err == nil {
		t.Fatal("sampled observer qualified absence")
	}
	half = 1
	raw.Attempts = []attemptTrace{{RequestID: strings.Repeat("a", 64), TraceID: strings.Repeat("c", 32)}}
	if err := os.WriteFile(filepath.Join(r.e.d.ObserverDir, r.id+".json"), mustJSON(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.costCapture(newNativeTally(r), nil, r.t0.Add(s.LoadEnd)); err == nil {
		t.Fatal("unmatched original physical contact qualified")
	}
}

func TestRequestCaptureIncludesFailedDirectAttemptAndActualRepeats(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	s := scenario.AlertingDayControlV1()
	r := &run{id: "fixture", t0: base, s: s, e: &Engine{d: Deps{CampaignNotifier: "sink", ObserverDir: t.TempDir()}}}
	r.opts.Provenance = &domain.CampaignProvenance{Role: domain.CampaignPreChangeControl, TargetBuild: "before", CorrTestBuild: "runner", WorkloadConfig: "routes"}
	v := newNativeTally(r)
	zero, one, cpu := 0, 1., 1.
	c := costFile{Run: r.id, Hash: s.Hash(), TargetBuild: "before", CorrTestBuild: "runner", WorkloadConfig: "routes", ObserverConfig: strings.Repeat("e", 64), From: base, Through: base.Add(s.LoadEnd), SamplingFraction: &one, ObserverCPUSeconds: &cpu, Routes: map[string]captureRoute{}}
	var receipts []targetops.Receipt
	for i, path := range []string{"direct", "canonical"} {
		recipient := "sink"
		if i == 0 {
			recipient = path
		}
		route := captureRoute{Receiver: path, Recipient: recipient, GroupIntervalNS: int64(time.Second), RepeatIntervalNS: int64(time.Minute), Dropped: &zero}
		member := lane.NativeMember{StartsAt: base, Labels: map[string]string{"campaign_lane": "native-" + path}}
		member.Receivers = append(member.Receivers, struct {
			Name string `json:"name"`
		}{Name: path})
		v.ordinary[path] = member
		clear := base.Add(90 * time.Second)
		v.clearAt[path] = clear
		for j, kind := range []string{"initial", "repeat", "resolve"} {
			at := []time.Time{base, base.Add(61 * time.Second), clear}[j]
			status := "firing"
			state := "f"
			if j == 2 {
				status = "resolved"
				state = "a"
			}
			members := []capturedMember{{path, status, base}}
			d := routeDecision{ID: fmt.Sprintf("%064x", i*4+j+1), Group: strings.Repeat("a", 64), GroupState: strings.Repeat(state, 64), Route: path, Kind: kind, Opened: base, At: at, NativeConfirmed: at.Add(time.Millisecond), Members: members}
			c.Decisions = append(c.Decisions, d)
			outcomes := []string{"accepted"}
			if i == 0 && j == 0 {
				outcomes = []string{"rejected", "accepted"}
			}
			for k, outcome := range outcomes {
				route.Through++
				n := len(c.Requests) + 1
				q := capturedRequest{RequestID: fmt.Sprintf("%064x", n), DecisionID: d.ID, Sequence: route.Through, Origin: "native_webhook", Receiver: path, Recipient: recipient, Started: at.Add(time.Duration(k) * time.Millisecond), Finished: at.Add(time.Duration(k+1) * time.Millisecond), Outcome: outcome, Members: members}
				if i == 1 {
					q.Origin = "oscar_recipient"
				}
				if outcome == "accepted" {
					q.ReceiptID = int64(n)
					receipts = append(receipts, targetops.Receipt{ID: int64(n), Receiver: path, Notifier: recipient, ReceivedAt: q.Finished, Alerts: []targetops.SinkAlert{{AlertName: path, Status: status, StartsAt: base.Format(time.RFC3339Nano)}}})
				}
				c.Requests = append(c.Requests, q)
				if j == 2 {
					continue
				}
				entry := "native_direct"
				if i == 1 {
					entry = "canonical_entry"
				}
				sql := 0
				spans := []traceSpan{{Category: entry}}
				if outcome == "rejected" {
					sql++
					spans = append(spans, traceSpan{Category: "journal_sql"})
				}
				c.Attempts = append(c.Attempts, attemptTrace{TraceID: fmt.Sprintf("%032x", n), RequestID: q.RequestID, Started: q.Started, Finished: q.Finished, SQLSeen: &sql, HTTPSeen: &zero, Dropped: &zero, Spans: spans})
			}
		}
		c.Routes[path] = route
	}
	save := func() {
		t.Helper()
		if err := os.WriteFile(filepath.Join(r.e.d.ObserverDir, r.id+".json"), mustJSON(c), 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	out, err := r.costCapture(v, receipts, c.Through)
	if err != nil || len(v.Missing) != 0 || out.Attempts["direct"] != 3 || out.Counts["direct"]["journal_sql"] != 1 || !slices.Contains(v.Failures, "never-enrolled direct attempt traversed the native journal") {
		t.Fatal(out, v.nativeEvidence, err)
	}
	q := c.Requests[0]
	c.Requests = c.Requests[1:]
	save()
	if _, err = r.costCapture(v, receipts, c.Through); err == nil {
		t.Fatal("omitted outbound failure qualified")
	}
	c.Requests = append([]capturedRequest{q}, c.Requests...)
	c.Decisions[1].At = base.Add(time.Second)
	v.Failures = nil
	if _, err = r.ordinaryContacts(v, c, receipts); err != nil || !slices.Contains(v.Failures, "ordinary repeat disagrees with native group or effective deadline") {
		t.Fatal("early duplicate filled repeat", err, v.Failures)
	}
}

func TestManagedRateOriginalVariantAndCapture(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	const historyID = "0b94baac-3055-45be-9697-2dcd6fa35fa4"
	// Drive the production query builder and bounded reader with fixed mysql
	// batch output. The temporary executable never connects to a database.
	dir := t.TempDir()
	queryPath := filepath.Join(dir, "prepared.sql")
	t.Setenv("CORRTEST_FIXTURE_QUERY", queryPath)
	t.Setenv("PATH", dir)
	const stub = `#!/bin/sh
/bin/cat > "$CORRTEST_FIXTURE_QUERY" || exit 1
exec /bin/cat testdata/managed-coalesced.snapshot
`
	if err := os.WriteFile(filepath.Join(dir, "mysql"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	config := managed.Config{MySQLDefaultsFile: filepath.Join(dir, "client.cnf")}
	if err := os.WriteFile(config.MySQLDefaultsFile, []byte("[client]"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := config.Snapshot(context.Background(), historyID)
	if err != nil {
		t.Fatal(err)
	}
	query, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatal(err)
	}
	sourceQuery, _, found := strings.Cut(string(query), "DEALLOCATE PREPARE")
	if !found || !strings.Contains(strings.ReplaceAll(sourceQuery, "''", "'"), "'Receiver',h.native_binding->>'$.receiver_id'") {
		t.Fatal("source query must project the receiver from the original signed History binding")
	}
	if len(snap.Source) != 2 || len(snap.Effects) != 1 {
		t.Fatalf("snapshot has %d source rows and %d effects", len(snap.Source), len(snap.Effects))
	}
	minor := snap.Source[0]
	f := NativeEpisodeFixture{History: observedHistory{ID: historyID, Labels: map[string]string{"aid": "A"}}, Source: snap.Source[1]}
	a := targetops.SinkAlert{Status: "firing", StartsAt: at.Format(time.RFC3339Nano), Labels: map[string]string{"aid": "A", "source_id": "src", "node": "ne", "correlation_key": "key", "severity": "major"}}
	// Two native source variants may coalesce to this one physical contact.
	rc := []targetops.Receipt{{ID: 1, ReceivedAt: at, Notifier: "CAMPAIGN_SINK", Alerts: []targetops.SinkAlert{a}}}
	valid := func() bool { ok, _ := matchManagedSink(snap, rc, "sid", "CAMPAIGN_SINK", f); return ok }
	if !valid() {
		t.Fatal("coalesced notification rejected")
	}
	b := a
	b.StartsAt = minor.Starts
	b.Labels = map[string]string{"aid": "A", "source_id": "src", "node": "ne", "correlation_key": "key", "severity": "minor"}
	child := snap.Effects[0]
	child.Child = "minor-child"
	child.Variant = 1
	child.Severity = "minor"
	snap.Effects = append(snap.Effects, child)
	rc[0].Alerts = append(rc[0].Alerts, b)
	if !valid() {
		t.Fatal("two attributable member variants in one grouped request")
	}
	snap.Effects = snap.Effects[:1]
	rc[0].Alerts = rc[0].Alerts[:1]
	snap.Effects[0].Receiver = "different-native-receiver"
	if valid() {
		t.Fatal("target receiver differs from source binding")
	}
	snap.Effects[0].Receiver = "canonical"
	rc[0].Notifier = "other"
	if valid() {
		t.Fatal("wrong destination")
	}
	rc[0].Notifier = "CAMPAIGN_SINK"
	rc[0].Alerts[0].StartsAt = minor.Starts
	if valid() {
		t.Fatal("wrong variant start")
	}
	proof := managed.Settlement{Children: []managed.OriginalChild{{Action: "notification", Notifier: "sid", State: "sent"}, {Name: "ALERTHISTORY", Action: "ticket", Notifier: "hid", State: "suppressed"}}}
	if err := checkNoTicketRoot(proof, "sid", map[string]string{"ALERTHISTORY": "hid"}); err != nil {
		t.Fatal(err)
	}
	proof.Children[1].State = "sent"
	if checkNoTicketRoot(proof, "sid", map[string]string{"ALERTHISTORY": "hid"}) == nil {
		t.Fatal("unexpected ticket")
	}
}
func TestLateRateAndUnreadOriginalsRetainAllOwners(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		r := &run{id: h.id, e: New(h.deps, h.clock)}
		m := managedLane{run: r, managedDeps: &managedDeps{originals: &originalReaderFixture{}}}
		f := NativeEpisodeFixture{History: observedHistory{ID: "history", EpisodeNumber: 3}}
		snap := managed.Snapshot{Dispatches: []managed.Dispatch{{Root: "no-callback"}, {Root: "late-rate"}}}
		if _, err := m.proveClosedRoots(ctx, f, snap, "sink"); err == nil {
			t.Fatal("unread originals reported as closed")
		}
		open, err := h.db.OpenMutations(ctx, h.id)
		if err != nil || len(open) != 2 {
			t.Fatal(open, err)
		}
		ids := map[string]bool{}
		for _, o := range open {
			ids[o.ObjectID] = true
		}
		if !ids["late-rate"] || !ids["no-callback"] {
			t.Fatal(open)
		}
	})
}

func TestBaselineEpisodesCannotBorrowEarlierPooledDelivery(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	r := &run{e: &Engine{d: Deps{CampaignNotifier: "sink"}}}
	v := newNativeTally(r)
	plan := lane.Plan{Events: []lane.Event{{Identity: "A", Seq: 0, Kind: lane.Firing}, {Identity: "A", Seq: 1, Kind: lane.Resolve}, {Identity: "A", Seq: 2, Kind: lane.Firing}, {Identity: "A", Seq: 3, Kind: lane.Resolve}}}
	in := score.Inputs{Ledger: []domain.LaneRow{{Seq: 0, Done: true, Status: 200, SentAt: at}, {Seq: 2, Done: true, Status: 200, SentAt: at.Add(time.Minute)}}, History: []oscar.HistoryRecord{{ID: "h", AlertName: "A", Status: "resolved", StartsAt: at}}, Receipts: []targetops.Receipt{{ID: 3, Notifier: "sink", ReceivedAt: at, Alerts: []targetops.SinkAlert{{AlertName: "A", StartsAt: at.Format(time.RFC3339Nano), Status: "firing"}}}}}
	baselineEpisodes(v, plan, in)
	if len(v.Missing) == 0 || v.Facets["baseline_delivered_episodes"] != 1 || v.Facets["baseline_planned_episodes"] != 2 {
		t.Fatal(v.nativeEvidence)
	}
}
func TestLoadScheduleExcludesDrainCatchupAndMidRunPause(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	load := 150 * time.Minute
	for _, row := range []struct {
		offset, actual time.Duration
		ok             bool
	}{{load - time.Second, load, false}, {45 * time.Minute, 47 * time.Minute, false}, {30 * time.Minute, 30*time.Minute + 2*time.Second, true}} {
		if got := withinLoadOffer(t0, load, row.offset, t0.Add(row.actual)); got != row.ok {
			t.Fatal(row, got)
		}
	}
}
func TestSegmentTailMeasuresFromIndependentDeadline(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	v := newNativeTally(&run{sinkID: "sink"})
	effect := managed.Effect{Operation: "op", ScheduleRoot: "root", Action: "notification", Notifier: "sink", ChildState: "sent", EnteredAt: at.Add(11 * time.Second).Format(time.RFC3339Nano), ConfirmedAt: at.Add(12 * time.Second).Format(time.RFC3339Nano)}
	s := managed.Snapshot{Dispatches: []managed.Dispatch{{Root: "root", DueAt: at.Add(8 * time.Second).Format(time.RFC3339Nano)}}, Effects: []managed.Effect{effect}}
	d := dispatchSamples{ExpectedDue: map[string]time.Time{"op": at}, Windows: map[string]dispatchWindow{"root": {Before: at, By: at.Add(10 * time.Second)}}}
	v.segmentTimes("POL", s, d)
	if len(v.Missing) != 0 || !slices.Equal(v.timing["segment_POL.dispatch_lag"], []float64{10}) {
		t.Fatal(v.timing, v.Missing)
	}
	s.Dispatches = append(s.Dispatches, managed.Dispatch{Root: "unobserved"})
	v.segmentTimes("POL", s, d)
	if len(v.Missing) == 0 {
		t.Fatal("good timing concealed missing sibling dispatch")
	}
}

type inhibitorAccountingFixture struct {
	suppressionClient
	posted []bool
}

func (f *inhibitorAccountingFixture) Inhibitor(_ context.Context, _ lane.SuppressionSubject, clear bool) error {
	f.posted = append(f.posted, clear)
	return nil
}

func TestFunctionalAndBulkReceiptsAndInhibitorSourceAccounting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		r := &run{id: h.id, e: New(h.deps, h.clock), s: scenario.ManagedAlertsSmokeV1(), sinkID: "sid"}
		r.t0 = r.now()
		client := &inhibitorAccountingFixture{}
		for _, clear := range []bool{false, true} {
			if err := r.inhibitorPost(context.Background(), client, lane.SuppressionSubject{}, clear); err != nil {
				t.Fatal(err)
			}
		}
		if !slices.Equal(client.posted, []bool{false, true}) || r.sourceTotals["native_inhibitor"].InLoad != 2 {
			t.Fatal(client.posted, r.sourceTotals)
		}
		v := newNativeTally(r)
		f := NativeEpisodeFixture{History: observedHistory{ID: "original", Labels: map[string]string{"aid": "A"}}}
		v.changed = f
		mk := func(id int64) targetops.Receipt {
			return targetops.Receipt{ID: id, Alerts: []targetops.SinkAlert{{Status: "firing", StartsAt: r.t0.Format(time.RFC3339Nano), Labels: map[string]string{"aid": "A"}}}}
		}
		v.consume(domain.CampaignEvent{Type: "native.managed.effects", Detail: mustJSON(map[string]any{"history": "original", "sink": []targetops.Receipt{mk(1), mk(2), mk(3)}})})
		v.consume(domain.CampaignEvent{Type: "native.bulk.baseline", Detail: mustJSON(map[string]any{"subject": f, "receipts": []targetops.Receipt{mk(3), mk(4)}})})
		v.finish()
		if v.PhysicalRequests != 4 || v.ProviderMemberReceipts != 4 {
			t.Fatal(v.nativeEvidence)
		}
	})
}

func TestConfirmedUnexpectedTicketFailsAndMultipleChildChangeIsVisible(t *testing.T) {
	r := &run{id: "fixture", s: scenario.ManagedAlertsSmokeV1()}
	tally := newNativeTally(r)
	o := originalOwner{Kind: "unexpected_ticket", History: "h", Episode: 1, Root: "root", Operation: "op", Child: "unexpected"}
	tally.consume(domain.CampaignEvent{Type: "native.unexpected_ticket", Detail: mustJSON(o)})
	if len(tally.Failures) != 1 {
		t.Fatal(tally.nativeEvidence)
	}
	first := managed.OriginalChild{Operation: "op", Root: "root", Child: "sink"}
	before := signatures(map[string]managed.OriginalReceipt{"root": {Children: []managed.OriginalChild{first}}})
	after := signatures(map[string]managed.OriginalReceipt{"root": {Children: []managed.OriginalChild{first, {Operation: "op", Root: "root", Child: "unexpected"}}}})
	if before["root"] == after["root"] {
		t.Fatal("extra child on same original operation hidden")
	}
}
