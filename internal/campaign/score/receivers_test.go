package score

import (
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func rxRow(run string, n int) oscar.HistoryRecord {
	sev := "warning"
	return oscar.HistoryRecord{Status: "firing", Severity: &sev, Labels: ReceiverLabels(run, n)}
}

func TestCheckReceiverRow(t *testing.T) {
	p := ReceiverProbe{Server: 35}
	if got := CheckReceiverRow("camp_x", p, rxRow("camp_x", 35)); got != "" {
		t.Errorf("exact row: %s", got)
	}
	r := rxRow("camp_x", 35)
	r.Labels["instance"] = "192.0.2.1" // what a trap would carry without the mapping
	delete(r.Labels, "meta_hostname")
	r.Status = "resolved"
	got := CheckReceiverRow("camp_x", p, r)
	for _, want := range []string{"instance=", "meta_hostname=", "status resolved"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if !strings.Contains(CheckReceiverRow("camp_x", ReceiverProbe{Server: 36}, rxRow("camp_x", 35)), "Source") {
		t.Error("another server's row passed")
	}
}

// TestJudgeReceivers (OSC-207 AC-6): 100% gates; missing evidence inconclusive.
func TestJudgeReceivers(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	good := func() *ReceiverStats {
		s := &ReceiverStats{Planned: 3, Bound: 60, Removed: true}
		for i, rx := range []string{"webhook", "trap", "smtp"} {
			s.Probes = append(s.Probes, ReceiverProbe{Receiver: rx, Name: rx, Server: 33 + i, SentAt: t0, Found: true, CreatedAt: t0.Add(time.Duration(i+1) * time.Second)})
		}
		return s
	}
	s := good()
	if f, i := JudgeReceivers(s); len(f)+len(i) != 0 || s.Receivers["smtp"].Landed != 1 || s.Receivers["smtp"].LatencyP95 != 3 {
		t.Fatalf("pass: %v %v %+v", f, i, s.Receivers)
	}
	for name, mod := range map[string]func(*ReceiverStats){
		"send error": func(s *ReceiverStats) { s.Probes[0].SendErr = "HTTP 502" },
		"lost":       func(s *ReceiverStats) { s.Probes[1].Found = false },
		"late":       func(s *ReceiverStats) { s.Probes[2].CreatedAt = t0.Add(61 * time.Second) },
		"wrong":      func(s *ReceiverStats) { s.Probes[1].Problem = "instance=\"x\"" },
	} {
		s := good()
		mod(s)
		if f, _ := JudgeReceivers(s); len(f) == 0 {
			t.Errorf("%s did not fail", name)
		}
	}
	for name, mod := range map[string]func(*ReceiverStats){
		"setup":       func(s *ReceiverStats) { s.Setup = "create: HTTP 500"; s.Probes = nil },
		"read":        func(s *ReceiverStats) { s.Read = "timeout" },
		"nothing":     func(s *ReceiverStats) { s.Probes = nil },
		"not removed": func(s *ReceiverStats) { s.Removed = false },
	} {
		s := good()
		mod(s)
		if f, i := JudgeReceivers(s); len(f) != 0 || len(i) == 0 {
			t.Errorf("%s: fails %v incon %v", name, f, i)
		}
	}
	if f, i := JudgeReceivers(&ReceiverStats{Skipped: "skipped (by option)", Planned: 9}); len(f)+len(i) != 0 {
		t.Error("a skipped lane is judged")
	}
}
