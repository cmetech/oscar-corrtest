package targetops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUserAPI drives the v4 task and report calls against a fake middleware:
// paths under /ext/mw/api/v1, the user key header, a 404 result as pending,
// the report found by key and the manifest read back by sha256.
func TestUserAPI(t *testing.T) {
	manifest := []byte(`{"parts":1}`)
	sum := sha256.Sum256(manifest)
	results := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/object/manifest.json" {
			_, _ = w.Write(manifest)
			return
		}
		if r.Header.Get("X-API-Key") != "user-key" || !strings.HasPrefix(r.URL.Path, "/ext/mw/api/v1/") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch p := strings.TrimPrefix(r.URL.Path, "/ext/mw/api/v1"); {
		case r.Method == http.MethodPost && p == "/tasks/run/monitor:check_connection:homelab":
			_, _ = w.Write([]byte(`{"celery_task_id":"c-1","status":"PENDING"}`))
		case p == "/tasks/result/c-1":
			if results++; results == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"task_id":"c-1","status":"SUCCESS"}`))
		case p == "/reports":
			_, _ = w.Write([]byte(`[{"id":"r-0","report_key":"other"},{"id":"r-1","report_key":"core-alarm-history-dump","setup_state":"approved"}]`))
		case r.Method == http.MethodPost && p == "/reports/r-1/run":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["request_id"] != "q-1" || body["start"] != "2026-11-24T12:00:00+00:00" || body["end"] != "2026-11-24T13:00:00+00:00" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			_, _ = w.Write([]byte(`{"run_id":"q-1","state":"artifact_admitted","outcome":"committed"}`))
		case p == "/reports/runs/q-1":
			_, _ = w.Write([]byte(`{"id":"q-1","state":"artifact_ready","error_code":null}`))
		case p == "/reports/runs/q-1/artifacts":
			_, _ = w.Write([]byte(`{"manifest":{"id":"m-1","sha256":"` + hex.EncodeToString(sum[:]) + `","size_bytes":11},"items":[{"id":"p-1","size_bytes":40}],"next":null}`))
		case p == "/reports/runs/q-1/artifacts/m-1/download":
			_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/object/manifest.json"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	u := UserAPI{Root: srv.URL, Key: "user-key", HTTP: srv.Client()}
	id, err := u.RunTask(ctx, "monitor:check_connection:homelab")
	if err != nil || id != "c-1" {
		t.Fatalf("run task: %q %v", id, err)
	}
	if _, done, err := u.TaskResult(ctx, id); done || err != nil {
		t.Fatalf("a 404 result is pending: %v %v", done, err)
	}
	if s, done, err := u.TaskResult(ctx, id); !done || s != "SUCCESS" || err != nil {
		t.Fatalf("result %q %v %v", s, done, err)
	}
	info, err := u.Report(ctx, "core-alarm-history-dump")
	if err != nil || info.ID != "r-1" || info.SetupState != "approved" {
		t.Fatalf("report %+v %v", info, err)
	}
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	if _, err := u.RunReport(ctx, info.ID, "q-1", t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _, err := u.ReportRunState(ctx, "q-1"); st != "artifact_ready" || err != nil {
		t.Fatalf("state %q %v", st, err)
	}
	m, err := u.ReportArtifacts(ctx, "q-1")
	if err != nil || len(m.Items) != 1 {
		t.Fatalf("artifacts %+v %v", m, err)
	}
	if err := u.VerifyManifest(ctx, "q-1", m); err != nil {
		t.Fatalf("verify: %v", err)
	}
	m.Manifest.SHA256 = strings.Repeat("0", 64)
	if err := u.VerifyManifest(ctx, "q-1", m); err == nil {
		t.Fatal("a manifest that does not match its listing must not verify")
	}
	if _, err := (UserAPI{Root: srv.URL, Key: "wrong", HTTP: srv.Client()}).Report(ctx, "x"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("refused key: %v", err)
	}
}
