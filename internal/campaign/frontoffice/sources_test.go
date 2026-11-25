package frontoffice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
)

var srcT0 = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

func replaySources(t *testing.T, h http.HandlerFunc, d time.Duration) []ActionResult {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	var res []ActionResult
	sp := spec
	sp.SourceFilters = true
	Run(context.Background(), srv.URL, srv.Client(), []User{{"fo-01", "k"}}, 50, 7, sp, &Sources{Since: srcT0.Add(-time.Minute), Worker: 32},
		&fakeClock{now: srcT0, limit: srcT0.Add(d)}, func(r ActionResult) { res = append(res, r) })
	return res
}

// history answers like OSCAR's /history: rows whose labels match the filter
// (by score.MatchesSourceFilter), from servers 1..40, plus a planted bad row
// when bad is set.
func history(bad bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var f struct {
			Items []struct{ Field, Operator, Value string }
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filter")), &f)
		if len(f.Items) == 0 || !strings.HasPrefix(f.Items[0].Field, "uisrc:") {
			_, _ = w.Write([]byte("{}"))
			return
		}
		action := map[string]string{"uisrc:source equals": "source_equals", "uisrc:source contains": "source_contains",
			"uisrc:source_ip startsWith": "source_ip_startswith", "uisrc:source_ip isNotEmpty": "source_ip_notempty"}[f.Items[0].Field+" "+f.Items[0].Operator]
		type label struct{ Label, Value string }
		var recs []map[string]any
		for n := 1; n <= 40; n++ {
			l := lane.ServerLabels(n)
			if !score.MatchesSourceFilter(action, f.Items[0].Value, l) {
				continue
			}
			var ls []label
			for k, v := range l {
				ls = append(ls, label{k, v})
			}
			recs = append(recs, map[string]any{"alertname": fmt.Sprint("A", n), "labels": ls})
		}
		if bad {
			recs = append(recs, map[string]any{"alertname": "wrong", "labels": []label{{"meta_hostname", "zz.home.arpa"}}}, map[string]any{"alertname": "parent", "labels": []label{{"alertname", "P"}, {"severity", "warning"}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_records": len(recs) + 100, "records": recs})
	}
}

// TestSourceActionsQueryUISRC (OSC-207 AC-2): with Sources the rotation adds
// the four actions; each sends its uisrc filter over [Since, now+5m] with no
// view_status, and instance_filter names a worker server's instance.
func TestSourceActionsQueryUISRC(t *testing.T) {
	seen := map[string]bool{}
	res := replaySources(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var f struct {
			Items []struct{ Field, Operator, Value string }
		}
		_ = json.Unmarshal([]byte(q.Get("filter")), &f)
		if len(f.Items) == 1 {
			it := f.Items[0]
			switch {
			case strings.HasPrefix(it.Field, "uisrc:"):
				seen[it.Field+" "+it.Operator] = true
				if q.Get("view_status") != "" || q.Get("start_datetime") != "2026-11-12T23:59:00Z" || q.Get("end_datetime") == "" {
					t.Errorf("window/status %v", q)
				}
				if it.Operator == "equals" {
					if n, err := fmt.Sscanf(it.Value, "ct-srv-%d", new(int)); err != nil || n != 1 || !strings.HasPrefix(it.Value, "ct-srv-") || it.Value > "ct-srv-32" {
						t.Errorf("equals %q", it.Value)
					}
				}
				if it.Operator == "startsWith" && it.Value != "198.51.100.1" {
					t.Errorf("startsWith %q", it.Value)
				}
				if it.Operator == "contains" && !slices.Contains(SourceFragments, it.Value) {
					t.Errorf("contains %q", it.Value)
				}
			case it.Field == "label:instance":
				if n, ok := lane.ServerOf(it.Value); !ok || n > 32 {
					t.Errorf("instance %q", it.Value)
				}
			}
		}
		history(false)(w, r)
	}, 2*time.Hour)
	if len(seen) != 4 {
		t.Fatalf("source filters seen %v", seen)
	}
	got := map[string]bool{}
	for _, r := range res {
		got[r.Action] = true
		if slices.Contains(score.SourceActions, r.Action) && (r.Source == nil || r.Source.Mismatch != 0 || r.Source.Rows == 0 || r.Source.Count != r.Source.Rows+100) {
			t.Errorf("%s result %+v", r.Action, r.Source)
		}
	}
	for _, a := range append(slices.Clone(score.FOActions), score.SourceActions...) {
		if !got[a] {
			t.Errorf("never emitted %s", a)
		}
	}
}

// TestSourceExactnessCountsMismatches (AC-3): a returned row whose derived
// value misses the filter is a mismatch; a row without labels is unverifiable.
func TestSourceExactnessCountsMismatches(t *testing.T) {
	n := 0
	for _, r := range replaySources(t, history(true), time.Hour) {
		if r.Source == nil {
			continue
		}
		n++
		if r.Source.Mismatch != 1 || r.Source.Unverifiable != 1 || r.Source.Value == "" && r.Action != "source_ip_notempty" || r.Source.End.Before(r.At) {
			t.Errorf("%s %+v", r.Action, r.Source)
		}
	}
	if n == 0 {
		t.Fatal("no source samples")
	}
}

func TestNoSourcesKeepsRotation(t *testing.T) {
	for _, r := range replay(t, ok, []User{{"fo-01", "k"}}, time.Hour) {
		if r.Source != nil || slices.Contains(score.SourceActions, r.Action) {
			t.Fatalf("source action without Sources: %+v", r)
		}
	}
}
