package score

import (
	"strings"
	"testing"
	"time"
)

// TestMaintenanceJudge (OSC-189 AC-5, AC-6, plan review F2): inside probes
// processed inside the window must read suppressed with the window's marker
// and never be ticketed; controls and after probes must not carry it; a
// probe processed after the end is not judged; missing evidence, a late edge
// read or a foreign window is INCONCLUSIVE; an extra applied pattern FAILs.
func TestMaintenanceJudge(t *testing.T) {
	start := time.Date(2026, 11, 25, 17, 10, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	ok := func() MaintenanceStats {
		return MaintenanceStats{CR: "ct-maint-x", Start: start, End: end,
			Edges: []MaintEdge{{Want: "scheduled", Got: "scheduled"}, {Want: "active", Got: "active"}, {Want: "expired", Got: "expired"}},
			Probes: []MaintProbe{
				{Kind: MaintInside, Name: "i1", Found: true, Status: "suppressed", Window: "ct-maint-x", CreatedAt: start.Add(time.Minute)},
				{Kind: MaintControl, Name: "c1", Found: true, Status: "firing", CreatedAt: start.Add(time.Minute)},
				{Kind: MaintAfter, Name: "a1", Found: true, Status: "firing", CreatedAt: end.Add(time.Minute)},
			}}
	}
	for _, tc := range []struct {
		name       string
		mut        func(*MaintenanceStats)
		fail, inco string
	}{
		{"pass", func(*MaintenanceStats) {}, "", ""},
		{"inside not suppressed", func(s *MaintenanceStats) { s.Probes[0].Status, s.Probes[0].Window = "firing", "" }, "i1 inside", ""},
		{"inside suppressed by another window", func(s *MaintenanceStats) { s.Probes[0].Window = "other" }, "i1 inside", ""},
		{"inside ticketed", func(s *MaintenanceStats) { s.Probes[0].Ticketed = true }, "ticketed inside", ""},
		{"inside processed after the end", func(s *MaintenanceStats) {
			s.Probes[0].Status, s.Probes[0].Window, s.Probes[0].Ticketed, s.Probes[0].CreatedAt = "firing", "", true, end.Add(3*time.Second)
		}, "", "no inside probe processed inside"},
		{"control marked", func(s *MaintenanceStats) { s.Probes[1].Status, s.Probes[1].Window = "suppressed", "ct-maint-x" }, "c1 (control)", ""},
		{"after marked", func(s *MaintenanceStats) { s.Probes[2].Status, s.Probes[2].Window = "suppressed", "ct-maint-x" }, "a1 (after)", ""},
		{"control suppressed by a foreign window", func(s *MaintenanceStats) { s.Probes[1].Status, s.Probes[1].Window = "suppressed", "other" }, "", `suppressed by window "other"`},
		{"row missing", func(s *MaintenanceStats) { s.Probes[2].Found = false }, "", "without a History row: a1"},
		{"edge wrong", func(s *MaintenanceStats) { s.Edges[1].Got = "scheduled" }, `window read "scheduled"`, ""},
		{"edge late", func(s *MaintenanceStats) { s.Edges[2].Got, s.Edges[2].Late = "", true }, "", "expired read too late"},
		{"bounds differ", func(s *MaintenanceStats) { s.Edges[0].Bounds = "17:11-17:16" }, "window bounds", ""},
		{"extra pattern applied", func(s *MaintenanceStats) { s.Applied = "patterns [ct-maint-x maint-mode-rules]" }, "applied patterns", ""},
		{"setup failed", func(s *MaintenanceStats) { s.Setup = "create pattern: HTTP 403" }, "", "did not run"},
		{"skipped", func(s *MaintenanceStats) { s.Skipped, s.Probes = "skipped (by option)", nil }, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ok()
			tc.mut(&s)
			fails, incon := s.Judge()
			check := func(what string, got []string, want string) {
				if want == "" && len(got) > 0 || want != "" && !strings.Contains(strings.Join(got, "|"), want) {
					t.Errorf("%s %v, want %q", what, got, want)
				}
			}
			check("fails", fails, tc.fail)
			check("incon", incon, tc.inco)
		})
	}
}
