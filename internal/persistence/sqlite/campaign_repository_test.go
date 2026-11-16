package sqlite

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func openCampaignDB(t *testing.T) *Database {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func newCampaign(t *testing.T, d *Database, id string) {
	t.Helper()
	err := d.CreateCampaign(context.Background(), domain.Campaign{
		ID: id, ScenarioID: "s", ScenarioHash: "h", Trigger: "cli", RunBy: "agent", TriggerIdentity: "me",
		Status: domain.CampaignPrecheck, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigration0006AppliesOnTopOf0005(t *testing.T) {
	ctx := context.Background()
	db := openTestSQL(t)
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() < "0006" {
			data, _ := embeddedMigrations.ReadFile("migrations/" + e.Name())
			old["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if err := migrate(ctx, db, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO targets(id, display_name, base_url, created_at, updated_at) VALUES('t','T','http://x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO runs(id, short_token, target_id, status, cleanup_status, harness_version, created_at, updated_at) VALUES('r','tok','t','QUEUED','NOT_REQUIRED','v','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(ctx, db, embeddedMigrations); err != nil {
		t.Fatal(err)
	}
	var n, runs int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 8 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("runs=%d err=%v", runs, err)
	}
	for _, table := range []string{"campaigns", "campaign_events", "campaign_prechecks", "campaign_runs", "campaign_mutations", "campaign_lane_ledger"} {
		var c int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&c); err != nil || c != 1 {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestCampaignTransitions(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	for _, next := range []domain.CampaignStatus{domain.CampaignRunning, domain.CampaignStopping, domain.CampaignDraining, domain.CampaignCleaningUp} {
		if err := d.TransitionCampaign(ctx, "c1", next, now, "to "+string(next)); err != nil {
			t.Fatalf("%s: %v", next, err)
		}
	}
	if err := d.CompleteCampaign(ctx, "c1", domain.VerdictPass, domain.CleanupClean, true, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := d.TransitionCampaign(ctx, "c1", domain.CampaignRunning, now, "x"); err == nil {
		t.Fatal("COMPLETED->RUNNING accepted")
	}
	if err := d.TransitionCampaign(ctx, "nope", domain.CampaignRunning, now, "x"); err != ErrNotFound {
		t.Fatalf("err=%v", err)
	}
	var events int
	if err := d.db.QueryRow(`SELECT count(*) FROM campaign_events WHERE campaign_id='c1' AND type='campaign.transition'`).Scan(&events); err != nil || events != 5 {
		t.Fatalf("events=%d err=%v", events, err)
	}
}

func TestMutationLogOpenUntilRestored(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	a, err := d.RecordMutation(ctx, domain.CampaignMutation{CampaignID: "c1", Kind: "notifier_disable", ObjectID: "n1", Prior: json.RawMessage(`{"on":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordMutation(ctx, domain.CampaignMutation{CampaignID: "c1", Kind: "sink_enable", ObjectID: "k1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordMutation(ctx, domain.CampaignMutation{CampaignID: "c1", Kind: "bogus", ObjectID: "k1"}); err == nil {
		t.Fatal("bad kind accepted")
	}
	if err := d.MarkMutationRestored(ctx, a, now); err != nil {
		t.Fatal(err)
	}
	open, err := d.OpenMutations(ctx, "c1")
	if err != nil || len(open) != 1 || open[0].ObjectID != "k1" {
		t.Fatalf("open=%+v err=%v", open, err)
	}
}

func TestLaneLedgerIntentThenOutcome(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	l := NewLaneLedger(d)
	e := lane.Event{Seq: 1, Kind: lane.Firing, Identity: "id", Severity: "critical", Instance: "i"}
	if err := l.Intent(ctx, "L", e, now); err != nil {
		t.Fatal(err)
	}
	rows, err := d.LaneLedger(ctx, "L")
	if err != nil || len(rows) != 1 || rows[0].Done {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := l.Intent(ctx, "L", e, now); err == nil {
		t.Fatal("duplicate intent accepted")
	}
	if err := l.Outcome(ctx, "L", 1, lane.Outcome{SentAt: now, Status: 202, LatencyMillis: 12}); err != nil {
		t.Fatal(err)
	}
	rows, _ = d.LaneLedger(ctx, "L")
	if !rows[0].Done || rows[0].Status != 202 || rows[0].LatencyMillis != 12 {
		t.Fatalf("rows=%+v", rows)
	}
	if err := l.Outcome(ctx, "L", 9, lane.Outcome{}); err == nil {
		t.Fatal("outcome without intent accepted")
	}
}

func TestInterruptedCampaignsListsRunningOnes(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	newCampaign(t, d, "run")
	newCampaign(t, d, "done")
	_ = d.TransitionCampaign(ctx, "run", domain.CampaignRunning, now, "r")
	_ = d.TransitionCampaign(ctx, "done", domain.CampaignRunning, now, "r")
	_ = d.TransitionCampaign(ctx, "done", domain.CampaignDraining, now, "d")
	_ = d.TransitionCampaign(ctx, "done", domain.CampaignCleaningUp, now, "c")
	if err := d.CompleteCampaign(ctx, "done", domain.VerdictPass, domain.CleanupClean, true, json.RawMessage(`{}`), json.RawMessage(`{}`), now); err != nil {
		t.Fatal(err)
	}
	got, err := d.InterruptedCampaigns(ctx)
	if err != nil || len(got) != 1 || got[0].ID != "run" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	all, err := d.ListCampaigns(ctx, domain.CampaignFilter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("all=%d err=%v", len(all), err)
	}
	done, _ := d.ListCampaigns(ctx, domain.CampaignFilter{Status: domain.CampaignCompleted})
	if len(done) != 1 || done[0].ID != "done" || done[0].Verdict != domain.VerdictPass || done[0].EndedAt.IsZero() {
		t.Fatalf("done=%+v", done)
	}
}

func TestInterruptedCampaignsIncludesInterruptedAndRecovering(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	newCampaign(t, d, "i")
	newCampaign(t, d, "r")
	_ = d.TransitionCampaign(ctx, "i", domain.CampaignInterrupted, now, "x")
	_ = d.TransitionCampaign(ctx, "r", domain.CampaignInterrupted, now, "x")
	if err := d.TransitionCampaign(ctx, "r", domain.CampaignRecovering, now, "x"); err != nil {
		t.Fatal(err)
	}
	got, err := d.InterruptedCampaigns(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestTransitionToCompletedRejected(t *testing.T) {
	ctx := context.Background()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	if err := d.TransitionCampaign(ctx, "c1", domain.CampaignCompleted, time.Now(), "x"); err == nil {
		t.Fatal("TransitionCampaign accepted COMPLETED")
	}
}

func TestMutationRestoredTwiceIsDistinctFromNotFound(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	id, _ := d.RecordMutation(ctx, domain.CampaignMutation{CampaignID: "c1", Kind: "sink_enable", ObjectID: "k"})
	_ = d.MarkMutationRestored(ctx, id, now)
	if err := d.MarkMutationRestored(ctx, id, now); err == nil || err == ErrNotFound {
		t.Fatalf("err=%v", err)
	}
	if err := d.MarkMutationRestored(ctx, 999, now); err != ErrNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestLaneLedgerAcceptsEveryEmittedErrorClass(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	l := NewLaneLedger(d)
	for i, class := range append([]string{""}, lane.ErrClasses...) {
		if err := l.Intent(ctx, "L", lane.Event{Seq: i, Kind: lane.Firing}, now); err != nil {
			t.Fatal(err)
		}
		if err := l.Outcome(ctx, "L", i, lane.Outcome{SentAt: now, ErrorClass: class, Missed: class == lane.ErrClassMissed}); err != nil {
			t.Fatalf("class %q: %v", class, err)
		}
	}
}

// SetCampaignCleanup changes only the cleanup status of a COMPLETED campaign
// and records the change in the audit trail.
func TestSetCampaignCleanup(t *testing.T) {
	ctx := context.Background()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	if err := d.SetCampaignCleanup(ctx, "c1", domain.CleanupClean, time.Now(), "x"); err == nil {
		t.Fatal("a campaign that is not COMPLETED must be refused")
	}
	if err := d.CompleteCampaign(ctx, "c1", domain.VerdictFail, domain.CleanupDirty, false, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := d.SetCampaignCleanup(ctx, "c1", domain.CleanupClean, time.Now(), "cleanup re-run: DIRTY -> CLEAN"); err != nil {
		t.Fatal(err)
	}
	c, _ := d.GetCampaign(ctx, "c1")
	if c.Cleanup != domain.CleanupClean || c.Verdict != domain.VerdictFail || c.Status != domain.CampaignCompleted {
		t.Fatalf("%+v", c)
	}
	evs, _ := d.RecentCampaignEvents(ctx, "c1", 10)
	if last := evs[len(evs)-1]; last.Type != "campaign.cleanup" || last.Summary != "cleanup re-run: DIRTY -> CLEAN" {
		t.Fatalf("last event %+v", last)
	}
}

// Migration 0007: campaign_mutations accepts pre-check B5's notifier_enable
// (0006's kind CHECK refused it) and keeps every existing row.
func TestMigration0007AcceptsNotifierEnable(t *testing.T) {
	ctx := context.Background()
	db := openTestSQL(t)
	old := fstest.MapFS{}
	entries, _ := fs.ReadDir(embeddedMigrations, "migrations")
	for _, e := range entries {
		if e.Name() < "0007" {
			data, _ := embeddedMigrations.ReadFile("migrations/" + e.Name())
			old["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if err := migrate(ctx, db, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO campaigns(id, scenario_id, scenario_hash, custom, trigger, run_by, trigger_identity, status, started_at) VALUES('c1','s','h',0,'cli','agent','me','COMPLETED','2026-01-01T00:00:00Z')`,
		`INSERT INTO campaign_mutations(campaign_id, kind, object_id, prior_json, recorded_at) VALUES('c1','sink_enable','n5','{"status":"disabled"}','2026-01-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO campaign_mutations(campaign_id, kind, object_id, recorded_at) VALUES('c1','notifier_enable','CAMPAIGN_SINK','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("0006 should refuse notifier_enable (the bug 0007 fixes)")
	}
	if err := migrate(ctx, db, embeddedMigrations); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO campaign_mutations(campaign_id, kind, object_id, recorded_at) VALUES('c1','notifier_enable','CAMPAIGN_SINK','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("notifier_enable after 0007: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO campaign_mutations(campaign_id, kind, object_id, recorded_at) VALUES('c1','bogus','x','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("unknown kinds must still be refused")
	}
	var ids string
	if err := db.QueryRow(`SELECT group_concat(id || ':' || kind) FROM campaign_mutations ORDER BY id`).Scan(&ids); err != nil || ids != "1:sink_enable,2:notifier_enable" {
		t.Fatalf("rows %q err %v", ids, err)
	}
	var idx int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='campaign_mutations_open_idx'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatal("open-mutations index missing after 0007")
	}
}

// A run the runtime created but never started (set B validation failures in
// camp_681887ad6d96c6ff left ~3.9M QUEUED) is not a campaign run to score or
// close incidents for.
func TestCampaignRunsSkipsRunsThatNeverStarted(t *testing.T) {
	ctx := context.Background()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	for _, q := range []string{
		`INSERT INTO targets(id, display_name, base_url, created_at, updated_at) VALUES('t','T','http://x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO runs(id, short_token, target_id, status, cleanup_status, harness_version, created_at, updated_at) VALUES('queued','tok1','t','QUEUED','NOT_REQUIRED','v','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO runs(id, short_token, target_id, status, verdict, cleanup_status, harness_version, started_at, created_at, updated_at) VALUES('ran','tok2','t','COMPLETED','PASS','CLEAN','v','2026-01-01T00:00:01Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
	} {
		if _, err := d.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"queued", "ran"} {
		if err := d.LinkCampaignRun(ctx, "c1", id, "setB_ticket:flood"); err != nil {
			t.Fatal(err)
		}
	}
	links, err := d.CampaignRuns(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].RunID != "ran" {
		t.Fatalf("links %+v", links)
	}
}

// Recovery reads the t0 and scenario events; camp_681887ad6d96c6ff buried them
// under ~3.9M later events, beyond any recent-events window.
func TestFirstCampaignEventFindsOldEventByType(t *testing.T) {
	ctx := context.Background()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	at := time.Now()
	if err := d.AppendCampaignEvent(ctx, "c1", "campaign.t0", "info", "t0", json.RawMessage(`{"T0":"2026-11-15T20:00:40Z"}`), at); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if err := d.AppendCampaignEvent(ctx, "c1", "campaign.correlation", "warn", "noise", nil, at); err != nil {
			t.Fatal(err)
		}
	}
	if recent, _ := d.RecentCampaignEvents(ctx, "c1", 10); len(recent) != 10 || recent[0].Type == "campaign.t0" {
		t.Fatalf("setup: t0 should be outside the recent window: %+v", recent)
	}
	ev, ok, err := d.FirstCampaignEvent(ctx, "c1", "campaign.t0")
	if err != nil || !ok || ev.Type != "campaign.t0" || string(ev.Detail) != `{"T0":"2026-11-15T20:00:40Z"}` {
		t.Fatalf("first t0 event: %+v %v %v", ev, ok, err)
	}
	if _, ok, err := d.FirstCampaignEvent(ctx, "c1", "campaign.missing"); ok || err != nil {
		t.Fatalf("missing type: ok=%v err=%v", ok, err)
	}
}

// The bundle export pages every event by sequence instead of a newest-N window.
func TestCampaignEventsAfterPagesBySequence(t *testing.T) {
	ctx := context.Background()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1") // sequence 1: campaign.created
	newCampaign(t, d, "c2")
	for i := 0; i < 24; i++ {
		if err := d.AppendCampaignEvent(ctx, "c1", "campaign.x", "info", "e", json.RawMessage(`{"i":1}`), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := d.AppendCampaignEvent(ctx, "c2", "campaign.other", "info", "e", nil, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var got []int
	for after := 0; ; {
		page, err := d.CampaignEventsAfter(ctx, "c1", after, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range page {
			if ev.CampaignID != "c1" {
				t.Fatalf("foreign event %+v", ev)
			}
			got = append(got, ev.Sequence)
			after = ev.Sequence
		}
		if len(page) < 10 {
			break
		}
	}
	if len(got) != 25 {
		t.Fatalf("paged %d events, want 25: %v", len(got), got)
	}
	for i, s := range got {
		if s != i+1 {
			t.Fatalf("sequence %d at %d, want ascending from 1: %v", s, i, got)
		}
	}
}

// Migration 0008: campaign_mutations accepts v2's queue_override and keeps every row.
func TestMigration0008AcceptsQueueOverride(t *testing.T) {
	ctx := context.Background()
	db := openTestSQL(t)
	old := fstest.MapFS{}
	entries, _ := fs.ReadDir(embeddedMigrations, "migrations")
	for _, e := range entries {
		if e.Name() < "0008" {
			data, _ := embeddedMigrations.ReadFile("migrations/" + e.Name())
			old["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if err := migrate(ctx, db, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO campaigns(id, scenario_id, scenario_hash, custom, trigger, run_by, trigger_identity, status, started_at) VALUES('c1','s','h',0,'cli','agent','me','COMPLETED','2026-01-01T00:00:00Z')`,
		`INSERT INTO campaign_mutations(campaign_id, kind, object_id, recorded_at) VALUES('c1','notifier_enable','CAMPAIGN_SINK','2026-01-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	insert := `INSERT INTO campaign_mutations(campaign_id, kind, object_id, prior_json, recorded_at) VALUES('c1','queue_override','alerts/ratelimits','{"queue":null}','2026-01-01T00:00:00Z')`
	if _, err := db.Exec(insert); err == nil {
		t.Fatal("0007 should refuse queue_override")
	}
	if err := migrate(ctx, db, embeddedMigrations); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert); err != nil {
		t.Fatalf("queue_override after 0008: %v", err)
	}
	var ids string
	if err := db.QueryRow(`SELECT group_concat(id || ':' || kind) FROM campaign_mutations ORDER BY id`).Scan(&ids); err != nil || ids != "1:notifier_enable,2:queue_override" {
		t.Fatalf("rows %q err %v", ids, err)
	}
}
