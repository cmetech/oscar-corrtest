package snmpload_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/cmetech/oscar-corrtest/internal/snmpload"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedPlanOwnsEveryLifecycleAndCapsTotalPacketRate(t *testing.T) {
	p, err := snmpload.Compile("crt_test_run", snmpload.Options{Port: 1162, Profile: "mixed", Rate: 10, Duration: 60, ClearDelay: 10, DrainTimeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	if p.OfferPackets != 600 || len(p.Packets) <= 600 {
		t.Fatalf("offer/tail accounting: %+v", p)
	}
	families := map[string]int{}
	states := map[int]string{}
	keys := map[string]bool{}
	last := int64(-100)
	for _, e := range p.Episodes {
		families[e.Family]++
		k := e.AlertName + "/" + e.AlertKey
		if keys[k] {
			t.Fatal("identity reused")
		}
		keys[k] = true
	}
	if len(families) != 4 {
		t.Fatalf("mix: %v", families)
	}
	repeats := 0
	for i, packet := range p.Packets {
		if packet.AtMillis-last < 100 {
			t.Fatalf("uncapped packet %d", i)
		}
		last = packet.AtMillis
		switch packet.Kind {
		case "problem":
			if states[packet.Episode] != "" {
				t.Fatal("duplicate problem")
			}
			states[packet.Episode] = "firing"
		case "repeat":
			repeats++
			if states[packet.Episode] != "firing" {
				t.Fatal("repeat after clear")
			}
		case "clear":
			if states[packet.Episode] != "firing" {
				t.Fatal("clear without problem")
			}
			states[packet.Episode] = "resolved"
		default:
			t.Fatal(packet.Kind)
		}
	}
	if repeats == 0 {
		t.Fatal("no repeats")
	}
	for _, s := range states {
		if s != "resolved" {
			t.Fatal("missing clear")
		}
	}
	q, _ := snmpload.Compile("crt_other_run", p.Options)
	if q.Episodes[0].AlertKey == p.Episodes[0].AlertKey {
		t.Fatal("cross-run identity collision")
	}
}

func TestRejectsInvalidLoadBounds(t *testing.T) {
	good := snmpload.Options{Port: 162, Profile: "mixed", Rate: 5, Duration: 60, ClearDelay: 30, DrainTimeout: 300}
	for _, change := range []func(*snmpload.Options){func(o *snmpload.Options) { o.Rate = 0 }, func(o *snmpload.Options) { o.Rate = 26 }, func(o *snmpload.Options) { o.Duration = 601 }, func(o *snmpload.Options) { o.ClearDelay = 0 }, func(o *snmpload.Options) { o.Port = 65536 }, func(o *snmpload.Options) { o.Profile = "arbitrary" }, func(o *snmpload.Options) { o.DrainTimeout = 1801 }} {
		o := good
		change(&o)
		if _, err := snmpload.Compile("crt_test", o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
}

func TestSmallMixedPlanIncludesAllFamiliesAndFullRunOwnership(t *testing.T) {
	o := snmpload.Options{Port: 162, Profile: "mixed", Rate: 1, Duration: 10, ClearDelay: 1, DrainTimeout: 10}
	p, err := snmpload.Compile("crt_7ZG2DFSJQJTWDGWWS2SA9DCGSE", o)
	if err != nil {
		t.Fatal(err)
	}
	families := map[string]bool{}
	for _, e := range p.Episodes {
		families[e.Family] = true
		if !strings.Contains(e.AlertKey, "crt_7ZG2DFSJQJTWDGWWS2SA9DCGSE") {
			t.Fatal("compressed source ownership")
		}
	}
	if len(families) != 4 {
		t.Fatalf("incomplete minimum mix: %v", families)
	}
	if _, err := snmpload.Fixture("crt_682XRE90T5SXA5ZXB8WMMC7RW0", 37, "smart-link"); err == nil {
		t.Fatal("numeric-only fixture allowed for bulk load")
	}
}

func TestRepeatsLeaveOneSecondBeforeClear(t *testing.T) {
	for _, delay := range []int{1, 2, 30} {
		p, err := snmpload.Compile("crt_repeat_gap", snmpload.Options{Port: 162, Profile: "mixed", Rate: 25, Duration: 60, ClearDelay: delay, DrainTimeout: 10})
		if err != nil {
			t.Fatal(err)
		}
		repeats := map[int]int64{}
		for _, packet := range p.Packets {
			if packet.Kind == "repeat" {
				repeats[packet.Episode] = packet.AtMillis
			}
			if packet.Kind == "clear" {
				if at, ok := repeats[packet.Episode]; ok && packet.AtMillis-at < 1000 {
					t.Fatalf("delay %d: repeat/clear gap %dms", delay, packet.AtMillis-at)
				}
			}
		}
	}
}

func TestFixtureSourceHashesAndMigratedTransitions(t *testing.T) {
	root := "../../../snmp-migration/output/oscar_rules"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("optional sibling migration corpus unavailable")
	}
	for _, family := range snmpload.Families {
		e, err := snmpload.Fixture("crt_source_parity", 0, family)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(root, e.MappingSource))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != e.MappingSHA256 {
			t.Fatalf("%s source changed; review fixture", family)
		}
		var mapping struct {
			Name     string `json:"mapping_name"`
			Elements []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"mapping_elements"`
		}
		if err := json.Unmarshal(raw, &mapping); err != nil {
			t.Fatal(err)
		}
		if mapping.Name != e.Mapping {
			t.Fatal("wrong enterprise mapping")
		}
		text := string(raw)
		for _, want := range []string{e.AlertName, e.AlertGroup, e.Agent, "hash~correlation_key", "add~resolution_mode", "trap_clear"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %s", family, want)
			}
		}
		problem := strings.TrimPrefix(e.ProblemOID, e.Mapping+".0.")
		clear := strings.TrimPrefix(e.ClearOID, e.Mapping+".0.")
		foundProblem, foundClear := false, false
		for _, element := range mapping.Elements {
			if strings.HasPrefix(element.Key, "if~specific_trap=="+problem+"~") && strings.Contains(element.Key, "add:severity:"+e.Severity) {
				foundProblem = true
			}
			if strings.HasPrefix(element.Key, "if~specific_trap=="+clear+"~") && strings.Contains(element.Key, "add:is_resolution:true") {
				foundClear = true
			}
		}
		if !foundProblem || !foundClear {
			t.Fatalf("%s missing expected problem/clear", family)
		}
	}
}
