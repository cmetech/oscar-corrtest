package engine

import (
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"testing"
	"time"
)

func TestManagedSinkRequiresOneReceiptPerPhysicalChild(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := managed.Effect{TargetState: "confirmed", Child: "original", Callback: "proof", Notifier: "sink", Action: "notification", Severity: "critical", EnteredAt: base.Format(time.RFC3339Nano), ConfirmedAt: base.Add(time.Second).Format(time.RFC3339Nano)}
	got := targetops.Receipt{ID: 1, ReceivedAt: base.Add(10 * time.Millisecond), Alerts: []targetops.SinkAlert{{Status: "firing", Labels: map[string]string{"severity": "critical", "correlation_key": "owned"}}}}
	if ok, err := matchSink([]managed.Effect{e}, []targetops.Receipt{got}, "sink", "owned"); err != nil || !ok {
		t.Fatal(err)
	}
	got2 := got
	got2.ID = 2
	if _, err := matchSink([]managed.Effect{e}, []targetops.Receipt{got, got2}, "sink", "owned"); err == nil {
		t.Fatal("duplicate physical receipt accepted")
	}
	if ok, _ := matchSink([]managed.Effect{e}, nil, "sink", "owned"); ok {
		t.Fatal("missing receipt accepted")
	}
}
