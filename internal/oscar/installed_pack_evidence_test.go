package oscar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func installedOfflineOriginal(t *testing.T) (InstalledPackExpected, InstalledPackBinding, InstalledPackObservation) {
	t.Helper()
	kit, _, _ := installedInputFixture(t, "https://unused.test")
	b := kit.Bindings()
	expected := InstalledPackExpected{ManifestSHA256: b.ManifestSHA256, PackID: b.PackID, Version: b.Version, PackContentHash: b.PackContentHash, MappingNamespace: b.ProfileNamespace, MappingName: b.ProfileName, MappingSourceFile: b.MappingSourceFile, MappingStateSHA256: b.MappingStateSHA256, MappingRowKey: b.MappingRowKey, RuleJSON: kit.RuleJSON(), MappingStateJSON: kit.MappingStateJSON()}
	var completed struct {
		Target   string `json:"target"`
		Manifest string `json:"manifest_sha256"`
		Load     string `json:"load_id"`
		Rule     int    `json:"rule_id"`
		Runtime  struct {
			LoadedAt    string `json:"loaded_at"`
			LastAttempt int64  `json:"last_attempt_ms"`
			IDs         []int  `json:"loaded_rule_ids"`
		} `json:"runtime_binding"`
	}
	root := "../testpack/testdata/installation/records"
	raw, e := os.ReadFile(filepath.Join(root, "completed.json"))
	if e != nil || json.Unmarshal(raw, &completed) != nil {
		t.Fatal(e)
	}
	original := InstalledPackBinding{TargetOrigin: completed.Target, ManifestSHA256: completed.Manifest, LoadID: completed.Load, RuleID: completed.Rule, Runtime: InstalledRuntimeBinding{LoadedAt: completed.Runtime.LoadedAt, LastAttemptMS: completed.Runtime.LastAttempt, LoadedRuleIDs: completed.Runtime.IDs}}
	bodies := make([]json.RawMessage, 0, 6)
	for _, number := range []int{19, 11, 20, 16, 17, 18} {
		raw, e = os.ReadFile(filepath.Join(root, fmt.Sprintf("%04d-after.json", number)))
		if e != nil {
			t.Fatal(e)
		}
		var after struct {
			Body json.RawMessage `json:"body"`
		}
		if e = json.Unmarshal(raw, &after); e != nil {
			t.Fatal(e)
		}
		bodies = append(bodies, after.Body)
	}
	return expected, original, InstalledPackObservation{Packs: bodies[0], Load: bodies[1], Mapping: bodies[2], Rules: bodies[3], Rule: bodies[4], Runtime: bodies[5]}
}
func TestInstalledOfflineOriginalSixReadValidation(t *testing.T) {
	e, b, o := installedOfflineOriginal(t)
	if err := ValidateInstalledPackObservation(e, b, o); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"https://owned-target.test/ext/mw", "https://owned-target.test"} {
		b.TargetOrigin = base
		if err := ValidateInstalledPackObservation(e, b, o); err != nil {
			t.Fatal(err)
		}
	}
}
func TestInstalledOfflineSixReadRefusals(t *testing.T) {
	for _, name := range []string{"packs", "load", "mapping", "rules", "rule", "runtime"} {
		for _, mutation := range []string{"missing", "null", "duplicate", "oversize", "trailing"} {
			t.Run(name+"/"+mutation, func(t *testing.T) {
				e, b, o := installedOfflineOriginal(t)
				fields := map[string]*json.RawMessage{"packs": &o.Packs, "load": &o.Load, "mapping": &o.Mapping, "rules": &o.Rules, "rule": &o.Rule, "runtime": &o.Runtime}
				p := fields[name]
				switch mutation {
				case "missing":
					*p = nil
				case "null":
					*p = []byte("null")
				case "duplicate":
					*p = append([]byte(`{"extra":1,"extra":2,`), (*p)[1:]...)
				case "oversize":
					*p = append(bytes.Repeat([]byte(" "), maxResponseBytes), (*p)...)
				case "trailing":
					*p = append(*p, []byte(` {}`)...)
				}
				if err := ValidateInstalledPackObservation(e, b, o); err == nil {
					t.Fatal("invalid original read accepted")
				}
			})
		}
	}
	for _, name := range []string{"base", "manifest", "rule-id", "loaded-at", "authoritative", "dispatch", "rule-json", "mapping-json", "runtime-current"} {
		t.Run(name, func(t *testing.T) {
			e, b, o := installedOfflineOriginal(t)
			switch name {
			case "base":
				b.TargetOrigin += "/ext/mw/"
			case "manifest":
				b.ManifestSHA256 = strings.Repeat("0", 64)
			case "rule-id":
				b.RuleID++
			case "loaded-at":
				b.Runtime.LoadedAt = "2026-10-17T00:00:01Z"
			case "authoritative":
				b.Runtime.Authoritative = true
			case "dispatch":
				b.Runtime.DispatchEnabled = true
			case "rule-json":
				e.RuleJSON = bytes.Replace(e.RuleJSON, []byte(`"min_count":5`), []byte(`"min_count":6`), 1)
			case "mapping-json":
				e.MappingStateJSON = bytes.Replace(e.MappingStateJSON, []byte(`"comment":null`), []byte(`"comment":"changed"`), 1)
			case "runtime-current":
				o.Runtime = bytes.Replace(o.Runtime, []byte(`"authoritative":false`), []byte(`"authoritative":true`), 1)
			}
			if err := ValidateInstalledPackObservation(e, b, o); err == nil {
				t.Fatal("changed binding/read accepted")
			}
		})
	}
}
func installedOfflineRows(rows []json.RawMessage) json.RawMessage {
	parts := make([][]byte, len(rows))
	for i, r := range rows {
		parts[i] = r
	}
	return append(append([]byte("["), bytes.Join(parts, []byte(","))...), ']')
}
func installedOfflineOracle(t *testing.T) (InstalledPackManifest, Rule, InstalledFloodEvidence, json.RawMessage, json.RawMessage) {
	t.Helper()
	m, r, e := oracleFixture(t)
	active := []json.RawMessage{}
	audit := []json.RawMessage{}
	for _, row := range e.Active {
		active = append(active, row.Raw)
	}
	for _, row := range e.Audit {
		audit = append(audit, row.Raw)
	}
	return m, r, e, installedOfflineRows(active), installedOfflineRows(audit)
}
func TestInstalledOfflineDecodeUsesActualOracle(t *testing.T) {
	m, r, want, active, audit := installedOfflineOracle(t)
	got, e := DecodeInstalledFloodEvidence(active, want.History.Pages, audit)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("decoder differs from actual row/history parsers")
	}
	if actual := EvaluateInstalledFlood(m, r, got); actual.Disposition != InstalledFloodEligible {
		t.Fatal(actual)
	}
	active[0] = '!'
	audit[0] = '!'
	want.History.Pages[0][0] = '!'
	if actual := EvaluateInstalledFlood(m, r, got); actual.Disposition != InstalledFloodEligible {
		t.Fatal("returned evidence aliases input", actual)
	}
}
func TestInstalledOfflineDecodeRefusesMalformedAndMissingEvidence(t *testing.T) {
	for _, name := range []string{"active-null", "active-object", "active-trailing", "active-too-large", "active-invalid-utf8", "active-duplicate-label", "audit-null", "audit-object", "audit-rule-alias", "audit-bad-time", "history-missing", "history-page-repeat", "history-count", "history-id", "history-large"} {
		t.Run(name, func(t *testing.T) {
			_, _, want, active, audit := installedOfflineOracle(t)
			pages := want.History.Pages
			switch name {
			case "active-null":
				active = []byte("null")
			case "active-object":
				active = []byte(`{}`)
			case "active-trailing":
				active = append(active, []byte(`[]`)...)
			case "active-too-large":
				active = bytes.Repeat([]byte(" "), installedHistoryMaxBytes+1)
			case "active-invalid-utf8":
				active = bytes.Replace(active, []byte("positive"), []byte{0xff}, 1)
			case "active-duplicate-label":
				active = bytes.Replace(active, []byte(`"alert_labels":[`), []byte(`"alert_labels":[{"name":"site","value":"positive"},`), 1)
			case "audit-null":
				audit = []byte("null")
			case "audit-object":
				audit = []byte(`{}`)
			case "audit-rule-alias":
				audit = bytes.Replace(audit, []byte(`"rule_id":7`), []byte(`"rule_id":7,"RULE_ID":7`), 1)
			case "audit-bad-time":
				audit = bytes.Replace(audit, []byte("2026-10-17T12:00:10Z"), []byte("bad"), 1)
			case "history-missing":
				pages = nil
			case "history-page-repeat":
				pages = append(pages, pages[0])
			case "history-count":
				pages[0] = bytes.Replace(pages[0], []byte(`"total_records":15`), []byte(`"total_records":16`), 1)
			case "history-id":
				pages[0] = bytes.Replace(pages[0], []byte(`"ID":"00000000-0000-4000-8000-000000000001"`), []byte(`"ID":{}`), 1)
			case "history-large":
				pages[0] = append(bytes.Repeat([]byte(" "), maxResponseBytes), pages[0]...)
			}
			if _, e := DecodeInstalledFloodEvidence(active, pages, audit); e == nil {
				t.Fatal("malformed/missing data decoded")
			}
		})
	}
}
func TestInstalledOfflineDecodePreservesAdverseData(t *testing.T) {
	m, r, want, active, audit := installedOfflineOracle(t)
	rows := []json.RawMessage{}
	for _, row := range want.Audit {
		rows = append(rows, row.Raw)
	}
	rows = append(rows, rows[0])
	audit = installedOfflineRows(rows)
	got, e := DecodeInstalledFloodEvidence(active, want.History.Pages, audit)
	if e != nil {
		t.Fatal(e)
	}
	if actual := EvaluateInstalledFlood(m, r, got); actual.Disposition != InstalledFloodAmbiguous {
		t.Fatal("duplicate audit collapsed", actual)
	}
	// Self-described row identity is parsing input, never authority for another
	// rule; the actual original rule remains the oracle's independent parameter.
	audit = bytes.ReplaceAll(audit, []byte(`"rule_id":7`), []byte(`"rule_id":8`))
	got, e = DecodeInstalledFloodEvidence(active, want.History.Pages, audit)
	if e != nil {
		t.Fatal(e)
	}
	if actual := EvaluateInstalledFlood(m, r, got); actual.Disposition == InstalledFloodEligible {
		t.Fatal("decoder granted rule authority")
	}
}

func installedOfflineHistoryPages(t *testing.T, count int) []json.RawMessage {
	t.Helper()
	_, _, e, _, _ := installedOfflineOracle(t)
	var template map[string]any
	if err := json.Unmarshal(e.History.Pages[0], &template); err != nil {
		t.Fatal(err)
	}
	row := template["records"].([]any)[0].(map[string]any)
	pages := []json.RawMessage{}
	for offset := 0; offset < count; offset += 100 {
		rows := []json.RawMessage{}
		for index := offset; index < count && index < offset+100; index++ {
			row["ID"] = fmt.Sprintf("00000000-0000-4000-8000-%012x", index+1)
			rows = append(rows, oracleJSON(t, row))
		}
		pages = append(pages, oracleJSON(t, map[string]any{"records": rows, "total_records": count, "total_pages": (count + 99) / 100, "page": offset/100 + 1, "per_page": 100}))
	}
	return pages
}
func TestInstalledOfflineHistorySharesCompletePaginationAndPreservesAmbiguity(t *testing.T) {
	m, r, _, active, audit := installedOfflineOracle(t)
	pages := installedOfflineHistoryPages(t, 101)
	e, err := DecodeInstalledFloodEvidence(active, pages, audit)
	if err != nil || len(e.History.Records) != 101 || len(e.History.Pages) != 2 {
		t.Fatal(e, err)
	}
	if actual := EvaluateInstalledFlood(m, r, e); actual.Disposition != InstalledFloodAmbiguous {
		t.Fatal("oversized fixed population became eligible", actual)
	}
	for _, name := range []string{"missing-tail", "duplicate-id", "changed-total", "wrong-page", "extra-page", "max-pages"} {
		t.Run(name, func(t *testing.T) {
			pages := installedOfflineHistoryPages(t, 101)
			switch name {
			case "missing-tail":
				pages = pages[:1]
			case "duplicate-id":
				pages[1] = bytes.Replace(pages[1], []byte("00000000-0000-4000-8000-000000000065"), []byte("00000000-0000-4000-8000-000000000001"), 1)
			case "changed-total":
				pages[1] = bytes.Replace(pages[1], []byte(`"total_records":101`), []byte(`"total_records":102`), 1)
			case "wrong-page":
				pages[1] = bytes.Replace(pages[1], []byte(`"page":2`), []byte(`"page":1`), 1)
			case "extra-page":
				pages = append(pages, pages[1])
			case "max-pages":
				for len(pages) <= maxListPages {
					pages = append(pages, pages[0])
				}
			}
			if _, err := DecodeInstalledFloodEvidence(active, pages, audit); err == nil {
				t.Fatal("invalid bounded History accepted")
			}
		})
	}
}
func TestInstalledOfflineEmptyEvidenceIsExplicitlyIncomplete(t *testing.T) {
	m, r, _, _, _ := installedOfflineOracle(t)
	page := json.RawMessage(`{"records":[],"total_records":0,"total_pages":0,"page":1,"per_page":100}`)
	evidence, err := DecodeInstalledFloodEvidence([]byte("[]"), []json.RawMessage{page}, []byte("[]"))
	if err != nil {
		t.Fatal(err)
	}
	if got := EvaluateInstalledFlood(m, r, evidence); got.Disposition != InstalledFloodIncomplete {
		t.Fatal(got)
	}
}
func TestInstalledOfflineBoundsBeforeUnboundedHydration(t *testing.T) {
	_, _, good, active, audit := installedOfflineOracle(t)
	t.Run("row-count", func(t *testing.T) {
		raw := append([]byte("["), bytes.Repeat([]byte(`{},`), maxListPages*100)...)
		raw = append(raw, []byte(`{}]`)...)
		if _, err := DecodeInstalledFloodEvidence(raw, good.History.Pages, audit); err == nil {
			t.Fatal("row count accepted")
		}
	})
	t.Run("aggregate-history", func(t *testing.T) {
		pages := installedOfflineHistoryPages(t, 501)
		for i := range pages {
			pages[i] = append(pages[i], bytes.Repeat([]byte(" "), 3<<20)...)
		}
		if _, err := DecodeInstalledFloodEvidence(active, pages, audit); err == nil {
			t.Fatal("aggregate History bytes accepted")
		}
	})
}
