package targetops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/receivers"
)

// fakeMappings is the middleware mapping API: GET ?name= answers a list,
// GET ?mapping_namespace_name= a {rows} page, POST creates, DELETE /<id>.
type fakeMappings struct {
	mu   sync.Mutex
	maps map[string]mappingWire // id -> mapping
}

func (f *fakeMappings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const p = "/ext/mw/api/v1/mapping-data/mapping"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == p && r.URL.Query().Get("name") != "":
		out := []mappingWire{}
		for _, m := range f.maps {
			if m.Name == r.URL.Query().Get("name") {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && r.URL.Path == p:
		out := []mappingWire{}
		for _, m := range f.maps {
			if strings.EqualFold(m.Namespace, r.URL.Query().Get("mapping_namespace_name")) {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": out, "total": len(out)})
	case r.Method == http.MethodPost && r.URL.Path == p:
		var m mappingWire
		_ = json.NewDecoder(r.Body).Decode(&m)
		m.ID = "id-" + m.Name
		m.Namespace = strings.ToLower(m.Namespace) // OSCAR stores it lower case
		f.maps[m.ID] = m
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(m)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, p+"/"):
		delete(f.maps, strings.TrimPrefix(r.URL.Path, p+"/"))
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func TestReceiverMappingLifecycle(t *testing.T) {
	f := &fakeMappings{maps: map[string]mappingWire{"other": {ID: "other", Name: receivers.TrapOID, Namespace: "ext_other"}}}
	rx := Receivers{C: testClient(t, f)}
	ctx := context.Background()
	if _, found, err := rx.Mapping(ctx, receivers.TrapNamespace, receivers.TrapOID); found || err != nil {
		t.Fatalf("a same-named mapping in another namespace was found: %v", err)
	}
	if err := rx.CreateMapping(ctx, receivers.TrapNamespace, receivers.TrapOID, "d", receivers.TrapMapping()); err != nil {
		t.Fatal(err)
	}
	els, found, err := rx.Mapping(ctx, receivers.TrapNamespace, receivers.TrapOID)
	if !found || err != nil || !reflect.DeepEqual(els, receivers.TrapMapping()) {
		t.Fatalf("read-back %v %v %v", els, found, err)
	}
	if err := rx.CreateMapping(ctx, receivers.ProfileNamespace, "ct-rx-abc", "d", receivers.WebhookProfile()); err != nil {
		t.Fatal(err)
	}
	if names, err := rx.MappingNames(ctx, receivers.ProfileNamespace, "ct-rx-"); err != nil || !reflect.DeepEqual(names, []string{"ct-rx-abc"}) {
		t.Fatalf("names %v %v", names, err)
	}
	for _, m := range [][2]string{{receivers.TrapNamespace, receivers.TrapOID}, {receivers.ProfileNamespace, "ct-rx-abc"}, {receivers.ProfileNamespace, "ct-rx-abc"}} {
		if err := rx.DeleteMapping(ctx, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.maps) != 1 {
		t.Fatalf("left %v", f.maps)
	}
}

// TestActiveProbesAndNativeResolve (OSC-210): the active-alert read asks
// the server to filter (lane, and run when given), pages until total_records,
// refuses a short read, and a resolve posts the exact labels and startsAt with
// endsAt 30 s back.
func TestActiveProbesAndNativeResolve(t *testing.T) {
	var posted []map[string]any
	short := false
	rec := func(name, run string) map[string]any {
		return map[string]any{"alert_labels": []map[string]string{{"name": "alertname", "value": name}, {"name": "campaign_run", "value": run}, {"name": "campaign_lane", "value": "receivers"}, {"name": "timestamp", "value": "t"}},
			"fingerprint_sample_reference": map[string]string{"starts_at": "2026-11-25T23:46:47.000Z"}, "starts_at": "2026-11-25T23:46:47Z"}
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ext/mw/api/v1/alerts/active-alerts":
			var f struct {
				Items []struct{ Field, Value string }
			}
			_ = json.Unmarshal([]byte(r.URL.Query().Get("filter")), &f)
			run := ""
			for _, it := range f.Items {
				if it.Field == "label:campaign_run" {
					run = it.Value
				}
			}
			all := [][]map[string]any{{rec("a", "camp_x")}, {rec("b", "camp_y")}}
			if run == "camp_x" {
				all = [][]map[string]any{{rec("a", "camp_x")}}
			}
			page := 1
			if r.URL.Query().Get("page") == "2" {
				page = 2
			}
			total := 0
			for _, p := range all {
				total += len(p)
			}
			if short {
				total++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"records": all[page-1], "total_records": total, "total_pages": len(all), "source": "alertmanager_native"})
		case r.URL.Path == "/ext/alertmanager/api/v2/alerts" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&posted)
		default:
			http.NotFound(w, r)
		}
	})
	c := testClient(t, h)
	srv := httptest.NewServer(h)
	defer srv.Close()
	rx := Receivers{C: c, Root: srv.URL, HTTP: srv.Client()}
	got, err := rx.ActiveProbes(context.Background(), "camp_x")
	if err != nil || len(got) != 1 || got[0].Labels["alertname"] != "a" || got[0].Labels["timestamp"] != "t" || got[0].StartsAt.IsZero() {
		t.Fatalf("run probes %+v %v", got, err)
	}
	if all, err := rx.ActiveProbes(context.Background(), ""); err != nil || len(all) != 2 {
		t.Errorf("all probes %+v %v", all, err)
	}
	short = true
	if _, err := rx.ActiveProbes(context.Background(), ""); err == nil {
		t.Error("a short read was accepted")
	}
	if err := rx.ResolveNative(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	end, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(posted[0]["endsAt"]))
	if len(posted) != 1 || posted[0]["startsAt"] != "2026-11-25T23:46:47Z" || time.Since(end) < 29*time.Second || posted[0]["labels"].(map[string]any)["timestamp"] != "t" {
		t.Errorf("posted %+v", posted)
	}
}
