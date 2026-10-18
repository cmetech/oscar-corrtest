package oscar

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func oracleJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	b, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func oracleFixture(t *testing.T) (InstalledPackManifest, Rule, InstalledFloodEvidence) {
	t.Helper()
	manifest, e := FreezeInstalledPackInput(installedPureSpec())
	if e != nil {
		t.Fatal(e)
	}
	rule := Rule{ID: 7, Name: "corrtest-operational-v1-flood", Pattern: "flood"}
	var template map[string]any
	raw, e := os.ReadFile("testdata/installed-history-page.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &template); e != nil {
		t.Fatal(e)
	}
	original := template["records"].([]any)[0]
	evidence := InstalledFloodEvidence{}
	rows := []any{}
	for i := 0; i < 15; i++ {
		site := "positive"
		if i >= 12 {
			site = "isolated-b"
		} else if i >= 9 {
			site = "isolated-a"
		} else if i >= 5 {
			site = "negative"
		}
		labels := map[string]string{"alertname": "CorrTestOperationalFlood", "qualification_pack": "corrtest_operational_v1", "qualification_run": strings.Repeat("a", 32), "site": site, "source_id": fmt.Sprintf("%s-%02d", strings.Repeat("a", 32), i), "severity": "warning"}
		native := fmt.Sprintf("%016x", i+1)
		start := "2026-10-17T12:00:00Z"
		activeLabels := []any{}
		historyLabels := []any{}
		for k, v := range labels {
			activeLabels = append(activeLabels, map[string]any{"name": k, "value": v})
			historyLabels = append(historyLabels, map[string]any{"Label": k, "Value": v})
		}
		active, e := installedActiveRecord(oracleJSON(t, map[string]any{"fingerprint": native, "starts_at": start, "alertname": "CorrTestOperationalFlood", "alert_labels": activeLabels}), strings.Repeat("a", 32))
		if e != nil {
			t.Fatal(e)
		}
		evidence.Active = append(evidence.Active, active)
		var h map[string]any
		if e = json.Unmarshal(oracleJSON(t, original), &h); e != nil {
			t.Fatal(e)
		}
		h["ID"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		h["fingerprint"] = fmt.Sprintf("oscar-%d", i)
		h["startsAt"] = start
		h["severity"] = "warning"
		historyLabels = append(historyLabels, map[string]any{"Label": "am_fingerprint", "Value": native}, map[string]any{"Label": "oscar_fingerprint", "Value": h["fingerprint"]})
		h["labels"] = historyLabels
		rows = append(rows, h)
		outcome := "enriched"
		if i == 4 {
			outcome = "parent_emitted"
		}
		audit, e := installedAuditRecord(oracleJSON(t, map[string]any{"id": i + 1, "created_at": "2026-10-17T12:00:10Z", "alert_fingerprint": native, "rule_id": 7, "rule_name": rule.Name, "pattern": "flood", "outcome": outcome, "window_id": "opaque-" + site, "reason": nil}), native, rule)
		if e != nil {
			t.Fatal(e)
		}
		evidence.Audit = append(evidence.Audit, audit)
	}
	template["records"] = rows
	template["total_records"] = 15
	evidence.History.Pages = []json.RawMessage{oracleJSON(t, template)}
	oracleHistoryTyped(t, &evidence)
	return manifest, rule, evidence
}
func oracleHistoryTyped(t *testing.T, e *InstalledFloodEvidence) {
	t.Helper()
	e.History.Records = nil
	for _, p := range e.History.Pages {
		var w struct {
			Records []historyWire `json:"records"`
		}
		if err := json.Unmarshal(p, &w); err != nil {
			t.Fatal(err)
		}
		for _, r := range w.Records {
			e.History.Records = append(e.History.Records, r.normalized())
		}
	}
}
func oracleHistoryChange(t *testing.T, e *InstalledFloodEvidence, index int, change func(map[string]any)) {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(e.History.Pages[0], &p); err != nil {
		t.Fatal(err)
	}
	change(p["records"].([]any)[index].(map[string]any))
	e.History.Pages[0] = oracleJSON(t, p)
	oracleHistoryTyped(t, e)
}
func oracleAuditChange(t *testing.T, e *InstalledFloodEvidence, index int, change func(map[string]any)) {
	t.Helper()
	var p map[string]any
	row := e.Audit[index]
	if err := json.Unmarshal(row.Raw, &p); err != nil {
		t.Fatal(err)
	}
	change(p)
	raw := oracleJSON(t, p)
	parsed, err := installedAuditRecord(raw, p["alert_fingerprint"].(string), Rule{ID: 7, Name: "corrtest-operational-v1-flood", Pattern: "flood"})
	if err != nil {
		t.Fatal(err)
	}
	e.Audit[index] = parsed
}
func oracleActiveChange(t *testing.T, e *InstalledFloodEvidence, index int, change func(map[string]any)) {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(e.Active[index].Raw, &p); err != nil {
		t.Fatal(err)
	}
	change(p)
	raw := oracleJSON(t, p)
	parsed, err := installedActiveRecord(raw, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	e.Active[index] = parsed
}

func TestInstalledFloodOracleCleanFixedIdentityAndAnyTrigger(t *testing.T) {
	for trigger := 0; trigger < 5; trigger++ {
		t.Run(fmt.Sprint(trigger), func(t *testing.T) {
			m, r, e := oracleFixture(t)
			oracleAuditChange(t, &e, 4, func(v map[string]any) { v["outcome"] = "enriched" })
			oracleAuditChange(t, &e, trigger, func(v map[string]any) { v["outcome"] = "parent_emitted" })
			for i, j := 0, len(e.Active)-1; i < j; i, j = i+1, j-1 {
				e.Active[i], e.Active[j] = e.Active[j], e.Active[i]
				e.Audit[i], e.Audit[j] = e.Audit[j], e.Audit[i]
			}
			before := oracleJSON(t, e)
			got := EvaluateInstalledFlood(m, r, e)
			if got.Disposition != InstalledFloodEligible {
				t.Fatalf("%+v", got)
			}
			if !reflect.DeepEqual(before, oracleJSON(t, e)) {
				t.Fatal("oracle mutated evidence")
			}
		})
	}
}
func TestInstalledFloodOracleIncompleteNeverEligible(t *testing.T) {
	for _, part := range []string{"active", "history", "audit"} {
		for n := 0; n < 15; n++ {
			t.Run(fmt.Sprintf("%s/%d", part, n), func(t *testing.T) {
				m, r, e := oracleFixture(t)
				switch part {
				case "active":
					e.Active = append(e.Active[:n], e.Active[n+1:]...)
				case "audit":
					e.Audit = append(e.Audit[:n], e.Audit[n+1:]...)
				case "history":
					var p map[string]any
					_ = json.Unmarshal(e.History.Pages[0], &p)
					rows := p["records"].([]any)
					p["records"] = append(rows[:n], rows[n+1:]...)
					p["total_records"] = 14
					e.History.Pages[0] = oracleJSON(t, p)
					oracleHistoryTyped(t, &e)
				}
				if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodIncomplete {
					t.Fatalf("%+v", got)
				}
			})
		}
	}
}
func TestInstalledFloodOracleViolations(t *testing.T) {
	cases := map[string]func(*InstalledFloodEvidence){
		"two-positive-triggers": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 0, func(v map[string]any) { v["outcome"] = "parent_emitted" })
		},
		"negative-trigger": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 5, func(v map[string]any) { v["outcome"] = "parent_emitted" })
		},
		"isolated-a-trigger": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 9, func(v map[string]any) { v["outcome"] = "parent_emitted" })
		},
		"isolated-b-trigger": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 12, func(v map[string]any) { v["outcome"] = "parent_emitted" })
		},
		"missing-positive-trigger": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 4, func(v map[string]any) { v["outcome"] = "enriched" })
		},
		"wrong-outcome": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 0, func(v map[string]any) { v["outcome"] = "suppressed_global" })
		},
		"split-window": func(e *InstalledFloodEvidence) {
			oracleAuditChange(t, e, 0, func(v map[string]any) { v["window_id"] = "other" })
		},
		"shared-window": func(e *InstalledFloodEvidence) {
			for i := 9; i < 12; i++ {
				oracleAuditChange(t, e, i, func(v map[string]any) { v["window_id"] = "opaque-negative" })
			}
		},
		"source-start": func(e *InstalledFloodEvidence) {
			oracleActiveChange(t, e, 0, func(v map[string]any) { v["starts_at"] = "2026-10-17T12:00:01Z" })
		},
		"source-group": func(e *InstalledFloodEvidence) {
			oracleActiveChange(t, e, 0, func(v map[string]any) {
				for _, l := range v["alert_labels"].([]any) {
					p := l.(map[string]any)
					if p["name"] == "site" {
						p["value"] = "negative"
					}
				}
			})
		},
		"history-native-link": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) {
				for _, l := range v["labels"].([]any) {
					p := l.(map[string]any)
					if p["Label"] == "am_fingerprint" {
						p["Value"] = "foreign"
					}
				}
			})
		},
		"history-oscar-link": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) { v["fingerprint"] = "foreign" })
		},
		"history-start": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) { v["startsAt"] = "2026-10-17T12:00:01Z" })
		},
		"history-severity": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) { v["severity"] = "major" })
		},
		"history-resolved": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) { v["status"] = "resolved" })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m, r, e := oracleFixture(t)
			change(&e)
			if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodViolation {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestInstalledFloodOracleAmbiguousEvidence(t *testing.T) {
	cases := map[string]func(*InstalledFloodEvidence){
		"insert-replay": func(e *InstalledFloodEvidence) {
			e.Audit = append(e.Audit, e.Audit[4])
			oracleAuditChange(t, e, 15, func(v map[string]any) { v["id"] = 99 })
		},
		"same-row-twice":        func(e *InstalledFloodEvidence) { e.Audit = append(e.Audit, e.Audit[4]) },
		"active-twice":          func(e *InstalledFloodEvidence) { e.Active = append(e.Active, e.Active[0]) },
		"typed-active":          func(e *InstalledFloodEvidence) { e.Active[0].Labels["source_id"] = "changed" },
		"typed-audit":           func(e *InstalledFloodEvidence) { e.Audit[0].WindowID = "changed" },
		"typed-history":         func(e *InstalledFloodEvidence) { e.History.Records[0].Labels["source_id"] = "changed" },
		"missing-raw-active":    func(e *InstalledFloodEvidence) { e.Active[0].Raw = nil },
		"missing-raw-audit":     func(e *InstalledFloodEvidence) { e.Audit[0].Raw = nil },
		"missing-history-pages": func(e *InstalledFloodEvidence) { e.History.Pages = nil },
		"history-count": func(e *InstalledFloodEvidence) {
			var p map[string]any
			_ = json.Unmarshal(e.History.Pages[0], &p)
			p["total_records"] = 16
			e.History.Pages[0] = oracleJSON(t, p)
		},
		"duplicate-history-id": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 1, func(v map[string]any) { v["ID"] = "00000000-0000-4000-8000-000000000001" })
		},
		"duplicate-history-label": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) {
				v["labels"] = append(v["labels"].([]any), map[string]any{"Label": "source_id", "Value": strings.Repeat("a", 32) + "-00"})
			})
		},
		"truncated-source": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) {
				for _, l := range v["labels"].([]any) {
					p := l.(map[string]any)
					if p["Label"] == "source_id" {
						p["value_truncated"] = true
						p["value_length"] = 100
					}
				}
			})
		},
		"length-mismatch": func(e *InstalledFloodEvidence) {
			oracleHistoryChange(t, e, 0, func(v map[string]any) {
				for _, l := range v["labels"].([]any) {
					p := l.(map[string]any)
					if p["Label"] == "am_fingerprint" {
						p["value_length"] = 100
					}
				}
			})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m, r, e := oracleFixture(t)
			change(&e)
			if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodAmbiguous {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestInstalledFloodOracleRefusesUnfixedContract(t *testing.T) {
	m, r, e := oracleFixture(t)
	for _, rule := range []Rule{{}, {ID: 7, Name: "foreign", Pattern: "flood"}, {ID: 7, Name: r.Name, Pattern: "absence"}} {
		if got := EvaluateInstalledFlood(m, rule, e); got.Disposition != InstalledFloodViolation {
			t.Fatalf("%+v", got)
		}
	}
	if got := EvaluateInstalledFlood(InstalledPackManifest{}, r, e); got.Disposition != InstalledFloodViolation {
		t.Fatalf("%+v", got)
	}
}
func TestInstalledFloodOracleHistoryPromotedSeverityAndMetadata(t *testing.T) {
	m, r, e := oracleFixture(t)
	oracleHistoryChange(t, &e, 0, func(v map[string]any) {
		rows := []any{}
		for _, l := range v["labels"].([]any) {
			p := l.(map[string]any)
			if p["Label"] != "severity" {
				p["value_truncated"] = false
				p["value_length"] = len(p["Value"].(string))
				rows = append(rows, p)
			}
		}
		v["labels"] = rows
		v["uninterpreted_metadata"] = "kept"
	})
	if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodEligible {
		t.Fatalf("%+v", got)
	}
}

func TestInstalledFloodOracleDuplicateOSCARIdentityIsAmbiguous(t *testing.T) {
	m, r, e := oracleFixture(t)
	oracleHistoryChange(t, &e, 1, func(v map[string]any) {
		v["fingerprint"] = "oscar-0"
		for _, item := range v["labels"].([]any) {
			label := item.(map[string]any)
			if label["Label"] == "oscar_fingerprint" {
				label["Value"] = "oscar-0"
			}
		}
	})
	if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodAmbiguous {
		t.Fatalf("same canonical History fingerprint hides distinct source episodes: %+v", got)
	}
}
func TestInstalledFloodOracleTruncationMetadataCannotBeHidden(t *testing.T) {
	for _, change := range []map[string]any{{"Value_Truncated": true}, {"value_truncated": nil}, {"value_truncated": "false"}, {"Value_Length": 99}, {"value_length": nil}, {"value_length": -1}, {"value_length": 1.5}} {
		t.Run(fmt.Sprint(change), func(t *testing.T) {
			m, r, e := oracleFixture(t)
			oracleHistoryChange(t, &e, 0, func(v map[string]any) {
				for _, item := range v["labels"].([]any) {
					label := item.(map[string]any)
					if label["Label"] == "qualification_run" {
						for k, value := range change {
							label[k] = value
						}
					}
				}
			})
			if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodAmbiguous {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestInstalledFloodOracleEmptyIsIncomplete(t *testing.T) {
	m, r, _ := oracleFixture(t)
	if got := EvaluateInstalledFlood(m, r, InstalledFloodEvidence{}); got.Disposition != InstalledFloodIncomplete {
		t.Fatalf("%+v", got)
	}
}
func TestInstalledFloodOracleForeignAuditIsViolation(t *testing.T) {
	m, r, e := oracleFixture(t)
	oracleAuditChange(t, &e, 0, func(v map[string]any) { v["alert_fingerprint"] = "ffffffffffffffff" })
	if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodViolation {
		t.Fatalf("%+v", got)
	}
}
func TestInstalledFloodOracleDoesNotDeriveNativeFromHistory(t *testing.T) {
	m, r, e := oracleFixture(t)
	for i := range e.Audit {
		n := i
		oracleAuditChange(t, &e, i, func(v map[string]any) { v["alert_fingerprint"] = fmt.Sprintf("oscar-%d", n) })
	}
	if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodViolation {
		t.Fatalf("%+v", got)
	}
}
func TestInstalledFloodOracleHistoryNormalizedOrderCannotHideRawMutation(t *testing.T) {
	m, r, e := oracleFixture(t)
	e.History.Records[0], e.History.Records[1] = e.History.Records[1], e.History.Records[0]
	if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodAmbiguous {
		t.Fatalf("%+v", got)
	}
}

func TestInstalledFloodOracleObservedViolationSurvivesUnrelatedMissingEvidence(t *testing.T) {
	for _, index := range []int{0, 5, 9, 12} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			m, r, e := oracleFixture(t)
			oracleAuditChange(t, &e, index, func(v map[string]any) { v["outcome"] = "parent_emitted" })
			e.History.Records = nil
			e.History.Pages = nil
			if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodViolation {
				t.Fatalf("observed wrong trigger hidden by pending History: %+v", got)
			}
		})
	}
}

func TestInstalledFloodOracleUnexpectedNativeLabelIsViolation(t *testing.T) {
	m, r, e := oracleFixture(t)
	oracleActiveChange(t, &e, 0, func(v map[string]any) {
		v["fingerprint"] = "eeeeeeeeeeeeeeee"
		v["alert_labels"] = append(v["alert_labels"].([]any), map[string]any{"name": "unexpected_identity", "value": "changed"})
	})
	oracleHistoryChange(t, &e, 0, func(v map[string]any) {
		for _, item := range v["labels"].([]any) {
			l := item.(map[string]any)
			if l["Label"] == "am_fingerprint" {
				l["Value"] = "eeeeeeeeeeeeeeee"
			}
		}
	})
	oracleAuditChange(t, &e, 0, func(v map[string]any) { v["alert_fingerprint"] = "eeeeeeeeeeeeeeee" })
	if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodViolation {
		t.Fatalf("extra Native label changed original input identity: %+v", got)
	}
}

func TestInstalledFloodOracleMissingHistoryAttributionIsAmbiguous(t *testing.T) {
	for _, field := range []string{"am_fingerprint", "oscar_fingerprint", "source_id", "fingerprint"} {
		for _, blank := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", field, blank), func(t *testing.T) {
				m, r, e := oracleFixture(t)
				oracleHistoryChange(t, &e, 0, func(v map[string]any) {
					if field == "fingerprint" {
						if blank {
							v[field] = " "
						} else {
							delete(v, field)
						}
						return
					}
					labels := []any{}
					for _, item := range v["labels"].([]any) {
						l := item.(map[string]any)
						if l["Label"] == field {
							if blank {
								l["Value"] = " "
								labels = append(labels, l)
							}
						} else {
							labels = append(labels, l)
						}
					}
					v["labels"] = labels
				})
				if got := EvaluateInstalledFlood(m, r, e); got.Disposition != InstalledFloodAmbiguous {
					t.Fatalf("missing attribution became product violation: %+v", got)
				}
			})
		}
	}
}
