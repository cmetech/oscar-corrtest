package targetops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
