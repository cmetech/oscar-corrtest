package lane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

// TestServerLabels (OSC-207 AC-1): documentation-only names and TEST-NET-2.
func TestServerLabels(t *testing.T) {
	want := map[string]string{"instance": "ct-srv-07.home.arpa:9100", "hostname": "ct-srv-07", "meta_hostname": "ct-srv-07.home.arpa", "meta_ipaddress": "198.51.100.17"}
	if got := ServerLabels(7); !reflect.DeepEqual(got, want) || ServerName(40) != "ct-srv-40" || ServerIP(40) != "198.51.100.50" {
		t.Errorf("labels %v", got)
	}
}

// TestCompileServers: Servers > 0 draws a server per worker alert from the
// same RNG step (seeded, deterministic), a pool identity keeps its server,
// and Servers == 0 leaves the plan exactly as before.
func TestCompileServers(t *testing.T) {
	spec := scenario.AlertingDay3psSmokeV2().Baseline
	base := Compile("camp_x", 7, spec, 10*time.Minute)
	spec.Servers = 32
	a, b := Compile("camp_x", 7, spec, 10*time.Minute), Compile("camp_x", 7, spec, 10*time.Minute)
	if !reflect.DeepEqual(a, b) || len(a.Events) != len(base.Events) {
		t.Fatal("not deterministic or a different plan size")
	}
	pool, seen := map[string]int{}, map[int]bool{}
	for i, e := range a.Events {
		n, ok := ServerOf(e.Instance)
		if !ok || n > 32 {
			t.Fatalf("event %d: %+v", i, e)
		}
		seen[n] = true
		if s, ok := pool[e.Identity]; ok && s != n {
			t.Fatalf("%s changed server %d -> %d", e.Identity, s, n)
		}
		pool[e.Identity] = n
		o := base.Events[i]
		o.Instance = e.Instance
		if !reflect.DeepEqual(o, e) {
			t.Fatalf("event %d differs beyond the server: %+v vs %+v", i, base.Events[i], e)
		}
	}
	if len(seen) < 30 {
		t.Errorf("only %d servers used", len(seen))
	}
	for _, e := range base.Events {
		if _, ok := ServerOf(e.Instance); ok {
			t.Fatal("Servers 0 drew a server")
		}
	}
	for _, bad := range []string{"ct-srv-07.home.arpa", "ct-srv-7.home.arpa:9100", "ct-srv-07.home.arpa:9100x", "campaign-host-07", "ct-srv-00.home.arpa:9100"} {
		if _, ok := ServerOf(bad); ok {
			t.Errorf("ServerOf(%q) matched", bad)
		}
	}
}

func TestSenderServerLabels(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewDecoder(r.Body).Decode(&got) }))
	defer srv.Close()
	s := NewAlertmanagerSenderClient(srv.URL, srv.Client())
	if _, err := s.Send(context.Background(), Event{Kind: Resolve, Identity: "A", Severity: "minor", Instance: ServerInstance(9)}, "camp_x", "l"); err != nil {
		t.Fatal(err)
	}
	labels := got[0]["labels"].(map[string]any)
	for k, v := range ServerLabels(9) {
		if labels[k] != v {
			t.Errorf("%s=%v want %s", k, labels[k], v)
		}
	}
	if _, err := s.Send(context.Background(), Event{Kind: Firing, Identity: "B", Severity: "minor", Instance: "campaign-host-01"}, "camp_x", "l"); err != nil {
		t.Fatal(err)
	}
	labels = got[0]["labels"].(map[string]any)
	if _, ok := labels["meta_ipaddress"]; ok || labels["instance"] != "campaign-host-01" || len(labels) != 6 {
		t.Errorf("non-server labels %v", labels)
	}
}
