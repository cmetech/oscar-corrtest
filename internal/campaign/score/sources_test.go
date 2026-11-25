package score

import (
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

var (
	st0    = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	srcLim = scenario.AlertingDay3psSmokeV2().Limits
)

func row(server string, created time.Duration) oscar.HistoryRecord {
	return oscar.HistoryRecord{CreatedAt: st0.Add(created), Labels: map[string]string{"meta_hostname": server + ".home.arpa", "meta_ipaddress": "198.51.100.17"}}
}

// okSamples answers every action once, correctly, at T+10m (rows of
// ct-srv-07 created at T+1m and T+2m: count 2).
func okSamples() []SourceSample {
	var out []SourceSample
	for _, a := range SourceActions {
		v := map[string]string{"source_equals": "ct-srv-07", "source_contains": "srv-0", "source_ip_startswith": "198.51.100.1"}[a]
		out = append(out, SourceSample{Action: a, Value: v, At: st0.Add(10 * time.Minute), End: st0.Add(10*time.Minute + 300*time.Millisecond),
			Seconds: 0.3, Status: 200, Answered: true, Count: 2, Rows: 2})
	}
	return out
}

func judge(t *testing.T, ss []SourceSample, hist []oscar.HistoryRecord, histOK bool) (SourceFilterStats, []Headline, []string, []string) {
	t.Helper()
	return JudgeSourceFilters(ss, hist, histOK, 250*time.Millisecond, srcLim)
}

func TestJudgeSourceFiltersPass(t *testing.T) {
	hist := []oscar.HistoryRecord{row("ct-srv-07", time.Minute), row("ct-srv-07", 2*time.Minute), row("ct-srv-17", 3*time.Minute), row("ct-srv-07", 11*time.Minute)}
	st, h, fails, incon := judge(t, okSamples(), hist, true)
	if len(fails)+len(incon) != 0 || len(h) != 2*len(SourceActions) || st.EqualsChecked != 1 {
		t.Fatalf("fails %v incon %v headlines %v stats %+v", fails, incon, h, st)
	}
	for _, x := range h {
		if !x.Pass || !strings.HasPrefix(x.Name, "frontoffice.source_") {
			t.Errorf("headline %+v", x)
		}
	}
	if p := st.Actions["source_equals"]; p.N != 1 || p.P95 != 0.3 {
		t.Errorf("percentiles %+v", p)
	}
}

// TestJudgeSourceEqualsWindow (AC-3): the count must lie between the rows
// created before the request (less 5 s and the skew) and the rows created by
// its end (plus the skew).
func TestJudgeSourceEqualsWindow(t *testing.T) {
	at := 10 * time.Minute
	for _, tc := range []struct {
		name  string
		rows  []time.Duration
		count int
		fail  bool
	}{
		{"exact", []time.Duration{time.Minute, 2 * time.Minute}, 2, false},
		{"in flight at the request", []time.Duration{time.Minute, at - 20*time.Second}, 1, false},
		{"landed during the request", []time.Duration{time.Minute, at + 200*time.Millisecond}, 2, false},
		{"lost row", []time.Duration{time.Minute, 2 * time.Minute}, 1, true},
		{"extra row", []time.Duration{time.Minute}, 2, true},
		{"row before the allowance missing", []time.Duration{time.Minute, at - 31*time.Second}, 1, true},
		{"row after the end counted", []time.Duration{time.Minute, at + time.Second}, 2, true},
	} {
		var hist []oscar.HistoryRecord
		for _, c := range tc.rows {
			hist = append(hist, row("ct-srv-07", c))
		}
		ss := okSamples()
		ss[0].Count = tc.count
		_, _, fails, _ := judge(t, ss, hist, true)
		if (len(fails) > 0) != tc.fail {
			t.Errorf("%s: fails %v", tc.name, fails)
		}
	}
}

func TestJudgeSourceFiltersFailAndInconclusive(t *testing.T) {
	hist := []oscar.HistoryRecord{row("ct-srv-07", time.Minute), row("ct-srv-07", 2*time.Minute)}
	ss := okSamples()
	ss[1].Mismatch = 1
	if _, _, fails, _ := judge(t, ss, hist, true); len(fails) != 1 || !strings.Contains(fails[0], "source_contains") {
		t.Errorf("mismatch: %v", fails)
	}
	ss = append(okSamples(), SourceSample{Action: "source_ip_notempty", Seconds: 0.5, Status: 503, Err: "http_5xx"})
	if _, _, fails, _ := judge(t, ss, hist, true); len(fails) != 1 || !strings.Contains(fails[0], "503") {
		t.Errorf("http error: %v", fails)
	}
	for name, mod := range map[string]func([]SourceSample) []SourceSample{
		"no samples": func(s []SourceSample) []SourceSample { return s[1:] },
		"only transport": func(s []SourceSample) []SourceSample {
			s[2] = SourceSample{Action: s[2].Action, Seconds: 0.5, Err: "timeout"}
			return s
		},
		"rate limited": func(s []SourceSample) []SourceSample {
			s[2] = SourceSample{Action: s[2].Action, Seconds: 0.5, Status: 429, Err: "http_4xx"}
			return s
		},
		"unverifiable":     func(s []SourceSample) []SourceSample { s[3].Unverifiable = 1; return s },
		"nothing to count": func(s []SourceSample) []SourceSample { s[0].At, s[0].End, s[0].Count = st0, st0, 0; return s },
	} {
		_, _, fails, incon := judge(t, mod(okSamples()), hist, true)
		if len(fails) != 0 || len(incon) == 0 {
			t.Errorf("%s: fails %v incon %v", name, fails, incon)
		}
	}
	ss = okSamples()
	ss[2].Seconds = 6 // over the 2 s p95 and 5 s p99 front-office limits
	if _, _, fails, _ := judge(t, ss, hist, true); len(fails) != 2 || !strings.Contains(fails[0]+fails[1], "frontoffice.source_ip_startswith.p95") {
		t.Errorf("slow: %v", fails)
	}
	if _, _, fails, incon := judge(t, okSamples(), nil, false); len(fails) != 0 || len(incon) != 1 || !strings.Contains(incon[0], "History") {
		t.Errorf("no history: %v %v", fails, incon)
	}
}
