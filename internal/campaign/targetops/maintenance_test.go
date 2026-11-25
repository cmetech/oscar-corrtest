package targetops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMaintenanceAPI (OSC-189) drives the maintenance calls against a fake
// middleware: paths under /ext/mw/api/v1/notifiers/maintenance, the user key,
// the request bodies OSCAR's schemas require, 404 on delete and disable as
// already gone, and a 403 surfaced as a StatusError.
func TestMaintenanceAPI(t *testing.T) {
	var got []string
	bodies := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "user-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/ext/mw/api/v1/notifiers/maintenance")
		got = append(got, r.Method+" "+p)
		if r.Body != nil {
			var b map[string]any
			if json.NewDecoder(r.Body).Decode(&b) == nil {
				bodies[r.Method+" "+p] = b
			}
		}
		switch r.Method + " " + p {
		case "GET /patterns":
			_, _ = w.Write([]byte(`{"items":[{"key":"maint-mode-rules","name":"legacy","match":[],"combinator":"or","always_apply":true,"used_by_live":0}],"problems":[]}`))
		case "GET /templates":
			_, _ = w.Write([]byte(`{"items":[],"problems":[]}`))
		case "POST /patterns", "POST /templates":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case "POST /enable":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"cr_number":"ct-maint-x","status":"enabled","message":"ok","patterns":["ct-maint-x"],"handlers":["alerthistory"]}`))
		case "GET ":
			if r.URL.Query().Get("q") != "ct-maint-" || r.URL.Query().Get("per_page") != "100" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`{"items":[{"id":"w1","cr_number":"ct-maint-a","status":"expired"}],"total":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"w2","cr_number":"ct-maint-b","status":"disabled"}],"total":2}`))
		case "GET /w1":
			_, _ = w.Write([]byte(`{"id":"w1","live_status":"absent"}`))
		case "GET /status/gone":
			_, _ = w.Write([]byte(`{"cr_number":"gone","is_active":false,"state":null,"suppression_id":null,"start_time":null}`))
		case "GET /status/ct-maint-x":
			_, _ = w.Write([]byte(`{"cr_number":"ct-maint-x","is_active":true,"state":"active","suppression_id":"s1","start_time":"2026-11-25T16:00:00Z","end_time":"2026-11-25T16:05:00Z"}`))
		case "DELETE /patterns/ct-maint-x":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /templates/gone", "POST /disable": // gone: OSCAR answers 404
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
		case "DELETE /patterns/denied":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"missing manage:maintenance-windows"}`))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()
	u := UserAPI{Root: srv.URL, Key: "user-key", HTTP: srv.Client()}
	ctx := context.Background()

	pats, err := u.MaintPatterns(ctx)
	if err != nil || len(pats) != 1 || !pats[0].AlwaysApply {
		t.Fatalf("patterns %+v %v", pats, err)
	}
	if tmpl, err := u.MaintTemplates(ctx); err != nil || len(tmpl) != 0 {
		t.Fatalf("templates %+v %v", tmpl, err)
	}
	p := MaintPattern{Key: "ct-maint-x", Name: "CorrTest", Match: []MaintMatch{{"campaign_maint", "equals", "x"}}, Combinator: "and"}
	if err := u.CreateMaintPattern(ctx, p); err != nil {
		t.Fatal(err)
	}
	if b := bodies["POST /patterns"]; b["always_apply"] != false || b["combinator"] != "and" || b["key"] != "ct-maint-x" {
		t.Errorf("pattern body %v", b)
	}
	if err := u.CreateMaintTemplate(ctx, MaintTemplate{Key: "ct-maint-x", Name: "CorrTest", ServerSelector: map[string]any{}, Servers: []string{},
		Patterns: []string{"ct-maint-x"}, DefaultHandlers: []string{"alerthistory"}}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC)
	en, err := u.EnableMaint(ctx, MaintWindow{CR: "ct-maint-x", Servers: []string{}, Patterns: []string{"ct-maint-x"}, Handlers: []string{"alerthistory"},
		Timezone: "UTC", Start: start, End: start.Add(5 * time.Minute), TemplateKey: "ct-maint-x"})
	if err != nil || en.Status != "enabled" || len(en.Patterns) != 1 || en.Handlers[0] != "alerthistory" {
		t.Fatalf("enable %+v %v", en, err)
	}
	if b := bodies["POST /enable"]; b["start_time"] != "2026-11-25T16:00:00Z" || b["end_time"] != "2026-11-25T16:05:00Z" || b["duration_hours"] != nil {
		t.Errorf("enable body %v", b)
	}
	if st, err := u.MaintStatus(ctx, "ct-maint-x"); err != nil || st.State != "active" || !st.Active {
		t.Fatalf("status %+v %v", st, err)
	}
	if st, err := u.MaintStatus(ctx, "ct-maint-x"); err != nil || st.Absent() {
		t.Fatalf("an existing window is not absent: %+v %v", st, err)
	}
	if st, err := u.MaintStatus(ctx, "gone"); err != nil || !st.Absent() {
		t.Fatalf("no window reads absent: %+v %v", st, err)
	}
	recs, err := u.MaintRecords(ctx, "ct-maint-")
	if err != nil || len(recs) != 2 || recs[1].CR != "ct-maint-b" {
		t.Fatalf("records (both pages) %+v %v", recs, err)
	}
	if live, err := u.MaintLive(ctx, "w1"); err != nil || live != "absent" {
		t.Fatalf("live %q %v", live, err)
	}
	if err := u.DeleteMaintPattern(ctx, "ct-maint-x"); err != nil {
		t.Fatal(err)
	}
	if err := u.DeleteMaintTemplate(ctx, "gone"); err != nil {
		t.Errorf("an absent template counts as deleted: %v", err)
	}
	if err := u.DisableMaint(ctx, "gone"); err != nil {
		t.Errorf("an absent window counts as disabled: %v", err)
	}
	var se *StatusError
	if err := u.DeleteMaintPattern(ctx, "denied"); !errors.As(err, &se) || se.Status != http.StatusForbidden {
		t.Errorf("403 must surface: %v", err)
	}
}
