package targetops

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestSNOW(t *testing.T, h http.HandlerFunc) *SNOW {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cred := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cred, []byte(`{"username":"u","password":"p"}`), 0o600)
	s, err := NewSNOW(srv.URL, cred, "")
	if err != nil {
		t.Fatal(err)
	}
	s.WakeInterval = 5 * time.Millisecond
	return s
}

func TestSnowCloseRequiresReadback(t *testing.T) {
	var patched string
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
			t.Error("basic auth missing")
		}
		switch {
		case r.Method == http.MethodPatch:
			b := make([]byte, 512)
			n, _ := r.Body.Read(b)
			patched = string(b[:n])
			w.Write([]byte(`{"result":{"state":"6"}}`)) // 200 but not actually applied
		case strings.HasSuffix(r.URL.Path, "/incident"):
			w.Write([]byte(`{"result":[{"number":"INC1","sys_id":"s1","state":"2"}]}`))
		case strings.HasSuffix(r.URL.Path, "/incident/s1"): // single-record GET returns an object
			w.Write([]byte(`{"result":{}}`))
		default:
			w.Write([]byte(`{"result":[]}`))
		}
	})
	_, err := s.Resolve(context.Background(), "INC1", "note")
	if err == nil || !strings.Contains(err.Error(), "readback") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(patched, `"close_code":"Resolved by caller"`) || !strings.Contains(patched, `"state":"6"`) || !strings.Contains(patched, `"close_notes":"note"`) {
		t.Fatalf("patch body %s", patched)
	}
}

func TestSnowResolveSucceedsWhenReadbackResolved(t *testing.T) {
	state := "2"
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			state = "6"
			w.Write([]byte(`{"result":{}}`))
		case strings.HasSuffix(r.URL.Path, "/incident"):
			w.Write([]byte(`{"result":[{"number":"INC1","sys_id":"s1","state":"` + state + `"}]}`))
		default:
			w.Write([]byte(`{"result":[{"value":"OSCAR resolved","sys_created_on":"2026-11-13 00:00:00"}]}`))
		}
	})
	inc, err := s.Resolve(context.Background(), "INC1", "n")
	if err != nil || inc.State != "6" || len(inc.Comments) != 1 {
		t.Fatal(inc, err)
	}
}

func TestSnowWakeRetriesUntilReady(t *testing.T) {
	calls := 0
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Write([]byte(`<html>Instance Hibernating</html>`))
			return
		}
		w.Write([]byte(`{"result":[]}`))
	})
	if err := s.Wake(context.Background(), 5*time.Second); err != nil || calls != 3 {
		t.Fatal(err, calls)
	}
}

func TestSnowOpenCampaignIncidentsExcludesCollidingRunAndBadRun(t *testing.T) {
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("sysparm_query")
		if !strings.Contains(q, "short_descriptionSTARTSWITHCampaign-run001-") || !strings.Contains(q, "stateNOT IN6,7,8") {
			t.Errorf("query %q", q)
		}
		w.Write([]byte(`{"result":[{"number":"INC2","sys_id":"s2","state":"1","short_description":"Campaign-run001-3"},` +
			`{"number":"INC3","sys_id":"s3","state":"1","short_description":"Campaign-run0010-1"}]}`))
	})
	got, err := s.OpenCampaignIncidents(context.Background(), "run001")
	if err != nil || len(got) != 1 || got[0].Number != "INC2" {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"", "r1", "a^b", "run 001", "run001%"} {
		if _, err := s.OpenCampaignIncidents(context.Background(), bad); err == nil {
			t.Fatalf("run %q should be rejected", bad)
		}
	}
}

func TestSnowIncidentJournalQuery(t *testing.T) {
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sys_journal_field") {
			q := r.URL.Query().Get("sysparm_query")
			if !strings.Contains(q, "element_id=s1") || !strings.Contains(q, "element=comments") {
				t.Errorf("journal query %q", q)
			}
			w.Write([]byte(`{"result":[{"value":"OSCAR resolution comment","sys_created_on":"2026-11-13 00:00:00"}]}`))
			return
		}
		w.Write([]byte(`{"result":[{"number":"INC1","sys_id":"s1","state":"2"}]}`))
	})
	inc, err := s.Incident(context.Background(), "INC1")
	if err != nil || len(inc.Comments) != 1 || inc.Comments[0].Value != "OSCAR resolution comment" {
		t.Fatal(inc, err)
	}
}

// Live PDI 2026-11-15: an itil account sees no sys_journal_field rows, while the
// incident's comments display value (account time zone, newest first) holds them.
func TestSnowIncidentCommentsFallBackToDisplayValueInUTC(t *testing.T) {
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sys_journal_field"):
			w.Write([]byte(`{"result":[]}`))
		case strings.HasSuffix(r.URL.Path, "/incident/s1"):
			if r.URL.Query().Get("sysparm_display_value") != "all" {
				t.Errorf("display query %v", r.URL.Query())
			}
			w.Write([]byte(`{"result":{"sys_created_on":{"value":"2026-11-15 15:28:40","display_value":"2026-11-15 07:28:40"},` +
				`"comments":{"value":"","display_value":"2026-11-15 07:30:10 - OSCAR oscar-ticketing (Comments)\nOSCAR alert still firing: episode 1, occurrence 3.\n\n` +
				`2026-11-15 07:29:05 - OSCAR oscar-ticketing (Comments)\n[OSCAR AUTO-UPDATE 2026-11-15 15:29:03 UTC] Alert has been resolved (Last severity: WARNING)\n\n"}}}`))
		default:
			w.Write([]byte(`{"result":[{"number":"INC1","sys_id":"s1","state":"1"}]}`))
		}
	})
	inc, err := s.Incident(context.Background(), "INC1")
	if err != nil || len(inc.Comments) != 2 {
		t.Fatal(inc, err)
	}
	first, second := inc.Comments[0], inc.Comments[1]
	if first.CreatedOn != "2026-11-15 15:29:05" || !strings.HasPrefix(first.Value, "[OSCAR AUTO-UPDATE") ||
		second.CreatedOn != "2026-11-15 15:30:10" || second.Value != "OSCAR alert still firing: episode 1, occurrence 3." {
		t.Fatalf("comments %+v, want oldest first in UTC", inc.Comments)
	}
}

func fakeIncidentPages(total int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		off, _ := strconv.Atoi(r.URL.Query().Get("sysparm_offset"))
		lim, _ := strconv.Atoi(r.URL.Query().Get("sysparm_limit"))
		if lim != 500 || !strings.Contains(r.URL.Query().Get("sysparm_query"), "ORDERBY") {
			http.Error(w, "bad paging", 400)
			return
		}
		var rows []string
		for i := off; i < off+lim && i < total; i++ {
			rows = append(rows, fmt.Sprintf(`{"number":"INC%d","sys_id":"s%d","state":"1","short_description":"Campaign-run001-%d"}`, i, i, i))
		}
		w.Write([]byte(`{"result":[` + strings.Join(rows, ",") + `]}`))
	}
}

func TestSnowOpenCampaignIncidentsPaginates(t *testing.T) {
	s := newTestSNOW(t, fakeIncidentPages(620))
	got, err := s.OpenCampaignIncidents(context.Background(), "run001")
	if err != nil || len(got) != 620 {
		t.Fatalf("got %d err=%v", len(got), err)
	}
}

func TestSnowOpenCampaignIncidentsCapErrors(t *testing.T) {
	s := newTestSNOW(t, fakeIncidentPages(20000))
	if _, err := s.OpenCampaignIncidents(context.Background(), "run001"); err == nil {
		t.Fatal("want cap error")
	}
}

func TestSnowOpenCorrTestIncidentsExactPrefixes(t *testing.T) {
	var queries []string
	s := newTestSNOW(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("sysparm_query")
		queries = append(queries, q)
		if !strings.Contains(q, "stateNOT IN6,7,8") {
			t.Errorf("query %q", q)
		}
		// The server returns colliding rows too; the client recheck drops them.
		w.Write([]byte(`{"result":[` +
			`{"number":"INC1","state":"1","short_description":"[CORRTEST][flood][TRIGGER][AB12CD34] p01: source alert 1 of 3"},` +
			`{"number":"INC2","state":"1","short_description":"[CORRTEST][flood][NON-TRIGGER][AB12CD34] n01: source alert 1 of 2"},` +
			`{"number":"INC3","state":"1","short_description":"[CORRTEST][flood][TRIGGER][AB12CD35] other run"},` +
			`{"number":"INC4","state":"1","short_description":"[CORRTEST][flood2][TRIGGER][AB12CD34] other pattern"}]}`))
	})
	got, err := s.OpenCorrTestIncidents(context.Background(), "flood", "ab12cd34")
	if err != nil {
		t.Fatal(err)
	}
	var nums []string
	for _, i := range got {
		nums = append(nums, i.Number)
	}
	if strings.Join(nums, ",") != "INC1,INC2" {
		t.Fatalf("incidents %v", nums)
	}
	if len(queries) != 2 || !strings.Contains(queries[0], "STARTSWITH[CORRTEST][flood][TRIGGER][AB12CD34]^") ||
		!strings.Contains(queries[1], "STARTSWITH[CORRTEST][flood][NON-TRIGGER][AB12CD34]^") {
		t.Fatalf("queries %v", queries)
	}
	for _, bad := range [][2]string{{"nosuch", "AB12CD34"}, {"flood", "AB12CD3"}, {"flood", "AB12CD3I"}, {"flood", "AB^2CD34"}} {
		if _, err := s.OpenCorrTestIncidents(context.Background(), bad[0], bad[1]); err == nil {
			t.Fatalf("%v should be rejected", bad)
		}
	}
}
