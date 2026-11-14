package targetops

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// TestLiveReadOnly runs read-only checks against the real target. Opt in with OSCAR_CORRTEST_LIVE_TARGETOPS=1 and
// OSCAR_LIVE_{BASE_URL,API_KEY_FILE,CA,SINK_URL,SINK_TOKEN_FILE,SINK_CA}. It performs no mutation.
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv("OSCAR_CORRTEST_LIVE_TARGETOPS") != "1" {
		t.Skip("set OSCAR_CORRTEST_LIVE_TARGETOPS=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	key, err := readSecret(os.Getenv("OSCAR_LIVE_API_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	base := os.Getenv("OSCAR_LIVE_BASE_URL") // https://oscar.home.arpa/ext/mw
	c, err := oscar.New(domain.Target{ID: "live", APIProfile: "public-v1", BaseURL: base, TLS: domain.TLSPolicy{CAPath: os.Getenv("OSCAR_LIVE_CA")}},
		oscar.Options{Getenv: func(string) string { return key }})
	if err != nil {
		t.Fatal(err)
	}
	all, err := listNotifiers(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := ExternalEnabled(ctx, c, []string{"ALERTHISTORY", "LOGGER", "CORRELATION_EVENTS", "CAMPAIGN_SINK"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("notifiers total=%d externalEnabled=%d", len(all), len(ext))
	var histID string
	for _, n := range all {
		t.Logf("  notifier name=%s type=%s status=%s", n.Name, n.Type, n.Status)
		if n.Name == "ALERTHISTORY" {
			histID = n.ID
		}
	}
	if histID != "" {
		st, err := Status(ctx, c, histID, "ticket", "")
		t.Logf("resend Status(ALERTHISTORY,ticket): enrollment=%q enabled=%v revision=%d open=%d err=%v", st.Enrollment, st.Enabled, st.Revision, st.Episodes.Open, err)
		two := 600
		def := PolicyDefinition{NotifierID: histID, Name: "livecheck", Action: "ticket", Mode: "time", IntervalSeconds: &two, Priority: 1,
			Condition: `campaign_run == "r1" and campaign_lane == "baseline"`}
		m, err := Preview(ctx, c, def, map[string]any{"campaign_run": "r1", "campaign_lane": "baseline"})
		t.Logf("preview matching sample: matched=%v err=%v", m, err)
		m, err = Preview(ctx, c, def, map[string]any{"campaign_run": "r2", "campaign_lane": "baseline"})
		t.Logf("preview non-matching sample: matched=%v err=%v", m, err)
	}
	runs, err := TaskRuns(ctx, c, []string{"anything"}, time.Now().Add(-time.Hour), time.Now())
	t.Logf("TaskRuns call ok=%v runs=%d", err == nil, len(runs))
	if err != nil {
		t.Logf("TaskRuns err: %v", err)
	}

	m, err := NewMetrics(os.Getenv("OSCAR_LIVE_VM_URL"), os.Getenv("OSCAR_LIVE_CA"), "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.Query(ctx, "up", time.Now())
	t.Logf("metrics up series=%d err=%v", len(v), err)

	s, err := NewSink(os.Getenv("OSCAR_LIVE_SINK_URL"), os.Getenv("OSCAR_LIVE_SINK_TOKEN_FILE"), os.Getenv("OSCAR_LIVE_SINK_CA"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := s.Receipts(ctx, "default", "no-such-run-livecheck", time.Now().Add(-24*time.Hour))
	t.Logf("sink receipts(no such run)=%d err=%v", len(rs), err)
}
