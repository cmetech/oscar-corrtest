package engine

import (
	"context"
	"strings"
	"testing"
)

// OSCAR labels its queue series queue_name. Scoring grouped them by "queue",
// so every queue merged into one series and any idle queue counted as drained
// (2026-11-16: delivery-retry@v1 scored "drained in 7 s" with tm_notifier 7,687 deep).
func TestQueueDrainIsJudgedPerOSCARQueue(t *testing.T) {
	h := newHarness(t)
	h.oscar.queueSeries = map[string]float64{"tm_notifier": 5000, "tm_internal": 0}
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, card := h.scorecard()
	if !strings.Contains(strings.Join(card.Reasons, "\n"), "queue tm_notifier did not drain") {
		t.Fatalf("verdict %s reasons %v", card.Verdict, card.Reasons)
	}
}
