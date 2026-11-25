package score

import (
	"encoding/json"
	"os"
	"testing"
)

// TestAlertSourceVectors pins the port to OSCAR's shared Source / Source IP
// vectors (testdata: a byte-identical copy of oscar-alertmanager
// tests/fixtures/alert-source-vectors.json at oscar 2b41fc39b).
func TestAlertSourceVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/alert-source-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Vectors []struct {
			Name, Source string
			SourceIP     string            `json:"source_ip"`
			Labels       map[string]string `json:"labels"`
		}
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Vectors) < 40 {
		t.Fatalf("vectors: %v (%d)", err, len(f.Vectors))
	}
	for _, v := range f.Vectors {
		if got := AlertSource(v.Labels); got != v.Source {
			t.Errorf("%s: source %q want %q", v.Name, got, v.Source)
		}
		if got := AlertSourceIP(v.Labels); got != v.SourceIP {
			t.Errorf("%s: source ip %q want %q", v.Name, got, v.SourceIP)
		}
	}
}

func TestMatchesSourceFilter(t *testing.T) {
	srv := func(n string, ip string) map[string]string {
		return map[string]string{"meta_hostname": "ct-srv-" + n + ".home.arpa", "meta_ipaddress": ip, "instance": "ct-srv-" + n + ".home.arpa:9100"}
	}
	for _, tc := range []struct {
		action, value string
		labels        map[string]string
		want          bool
	}{
		{"source_equals", "ct-srv-07", srv("07", "198.51.100.17"), true},
		{"source_equals", "CT-SRV-07", srv("07", "198.51.100.17"), true},
		{"source_equals", "ct-srv-07", srv("17", "198.51.100.27"), false},
		{"source_equals", "ct-srv-07", map[string]string{"instance": "ct-srv-07.home.arpa:9100"}, false}, // Source is the whole instance host
		{"source_contains", "srv-1", srv("13", "198.51.100.23"), true},
		{"source_contains", "srv-1", srv("21", "198.51.100.31"), false},
		{"source_ip_startswith", "198.51.100.1", srv("05", "198.51.100.15"), true},
		{"source_ip_startswith", "198.51.100.1", srv("10", "198.51.100.20"), false},
		{"source_ip_notempty", "", srv("10", "198.51.100.20"), true},
		{"source_ip_notempty", "", map[string]string{"instance": "campaign-host-01"}, false},
	} {
		if got := MatchesSourceFilter(tc.action, tc.value, tc.labels); got != tc.want {
			t.Errorf("%s %q %v: %v", tc.action, tc.value, tc.labels, got)
		}
	}
	for _, a := range SourceActions {
		if f, op := SourceFilter(a); f == "" || op == "" {
			t.Errorf("%s has no filter", a)
		}
	}
}
