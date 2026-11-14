package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
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
	d := openCampaignDB(t)
	var n int
	if err := d.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 6 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCampaignTransitions(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d := openCampaignDB(t)
	newCampaign(t, d, "c1")
	for _, next := range []domain.CampaignStatus{domain.CampaignRunning, domain.CampaignStopping, domain.CampaignDraining, domain.CampaignCleaningUp, domain.CampaignCompleted} {
		if err := d.TransitionCampaign(ctx, "c1", next, now, "to "+string(next)); err != nil {
			t.Fatalf("%s: %v", next, err)
		}
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
	if all[0].ID != "done" && all[1].Verdict != domain.VerdictPass && all[0].Verdict != domain.VerdictPass {
		t.Fatal("verdict lost")
	}
}
