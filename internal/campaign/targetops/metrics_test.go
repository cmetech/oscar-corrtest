package targetops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetricsQueryAndSnapshotIncludesAllQueries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("no bearer configured")
		}
		switch r.URL.Path {
		case "/ext/vmdb/api/v1/query":
			w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"a"},"value":[1700000000,"1"]},{"metric":{"job":"b"},"value":[1700000000,"0"]}]}}`))
		case "/ext/vmdb/api/v1/query_range":
			w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"q":"` + r.URL.Query().Get("query") + `"},"values":[[1,"2"],[2,"3"]]}]}}`))
		}
	}))
	defer srv.Close()
	m, _ := NewMetrics(srv.URL+"/ext/vmdb", "", "")
	v, err := m.Query(context.Background(), "up", time.Now())
	if err != nil || len(v) != 2 || v[0].Value != 1 || v[1].Metric["job"] != "b" {
		t.Fatal(v, err)
	}
	now := time.Now()
	raw, err := m.Snapshot(context.Background(), map[string]string{"one": "q1", "two": "q2"}, now.Add(-time.Hour), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	json.Unmarshal(raw, &got)
	if len(got) != 2 || got["one"] == nil || got["two"] == nil {
		t.Fatalf("snapshot = %s", raw)
	}
	mx, err := m.QueryRange(context.Background(), "q", now.Add(-time.Hour), now, time.Minute)
	if err != nil || len(mx) != 1 || len(mx[0].Values) != 2 || mx[0].Values[1].Value != 3 {
		t.Fatal(mx, err)
	}
}

func TestMetricsSnapshotFailsIfAnyQueryFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	m, _ := NewMetrics(srv.URL, "", "")
	if _, err := m.Snapshot(context.Background(), map[string]string{"a": "x"}, time.Now(), time.Now(), time.Minute); err == nil {
		t.Fatal("want error")
	}
}
