package runtime

import (
	"context"
	"encoding/json"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrapLoadMappedHistoryAndDurableSendLedger(t *testing.T) {
	for _, mode := range []string{"complete", "mixed", "cancel", "cancel-clear", "missing-preflight", "slow-sender"} {
		t.Run(mode, func(t *testing.T) { testTrapLoad(t, mode) })
	}
}
func testTrapLoad(t *testing.T, mode string) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	expectedPass := mode == "complete" || mode == "mixed"
	profile := "dhcp-limit"
	if mode == "mixed" {
		profile = "mixed"
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	actualPython, err := exec.Command(python, "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Fatal(err)
	}
	python = strings.TrimSpace(string(actualPython))
	// The fake receiver implements the independently specified DHCP mapping, not the planner.
	script := "#!" + python + `\n`
	script = strings.ReplaceAll(script, `\n`, "\n") + `import sys,os,json,time
p=os.environ['TRAP_LOAD_STATE'];a=sys.argv;oid=a[7];v=a[10::3]
if os.environ.get('TRAP_LOAD_MODE')=='slow-sender':time.sleep(1.2)
if os.environ.get('TRAP_LOAD_MODE')=='cancel-clear' and oid.endswith('.0.3') and '-pre-' not in v[0] and not os.path.exists(p+'.clear-started'):
 open(p+'.clear-started','w').write(v[0]); time.sleep(5)
assert 'load-private' not in a
assert os.path.isfile(os.environ['SNMPCONFPATH']+'/snmp.conf')
try: rows=json.load(open(p))
except FileNotFoundError: rows={}
severity='major'
if oid.startswith('1.3.6.1.4.1.2636.3.62.62.2.3.'):
 name='jnx_jdhcpv6_local_server_interface_limit_status';group='jnxJdhcpv6LocalServerInterfaceLimitStatus';agent='JUNIPER-JDHCPV6-MIB';clear='.0.2'
 key='jnxJdhcpv6RouterName:'+v[0]+', jnxJdhcpv6LocalServerInterfaceName:'+v[1]
elif oid.startswith('1.3.6.1.4.1.2636.3.61.61.1.3.'):
 name='jnx_jdhcp_local_server_interface_limit_status';group='jnxJdhcpLocalServerInterfaceLimitStatus';agent='JUNIPER-JDHCP-MIB';clear='.0.3'
 key='jnxJdhcpRouterName:'+v[0]+', jnxJdhcpLocalServerInterfaceName:'+v[1]
elif oid.endswith(('.0.3','.0.4')):
 name='jnx_access_auth_server_status';group='jnxAccessAuthServerStatus';agent='JUNIPER-USER-AAA-MIB';clear='.0.4';severity='critical';key='jnxUserAAAServerName:'+v[0]
else:
 name='jnx_access_auth_address_pool_threshold_status';group='jnxAccessAuthAddressPoolThresholdStatus';agent='JUNIPER-USER-AAA-MIB';clear='.0.6';key='jnxUserAAAAddressPoolName:'+v[0]
rows[key]={'id':v[0], 'fingerprint':v[0], 'alertname':name,'original_severity':severity,'severity':severity,'status':'resolved' if oid.endswith(clear) else 'firing','labels':[{'Label':'agent','Value':agent},{'Label':'alert_group','Value':group},{'Label':'alert_key','Value':key}]}
with open(p+'.tmp','w') as f:json.dump(rows,f)
os.replace(p+'.tmp',p)
`
	if err := os.WriteFile(filepath.Join(dir, "snmptrap"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TRAP_LOAD_STATE", state)
	t.Setenv("TRAP_LOAD_MODE", mode)
	t.Setenv("CORRTEST_SNMP_COMMUNITY", "load-private")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ext/mw/api/v1/alerts/history" {
			http.NotFound(w, req)
			return
		}
		rows := map[string]json.RawMessage{}
		raw, _ := os.ReadFile(state)
		_ = json.Unmarshal(raw, &rows)
		records := []json.RawMessage{}
		var filter struct {
			Items []struct {
				Value string `json:"value"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(req.URL.Query().Get("filter")), &filter); err != nil || len(filter.Items) != 1 {
			t.Error("missing exact History name filter")
			http.Error(w, "filter", 400)
			return
		}
		if mode != "missing-preflight" {
			for _, v := range rows {
				var row struct {
					AlertName string `json:"alertname"`
				}
				if err := json.Unmarshal(v, &row); err != nil {
					t.Error(err)
				}
				if filter.Items[0].Value == row.AlertName {
					records = append(records, v)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"total_records": len(records), "total_pages": 1, "per_page": 100, "records": records})
	}))
	defer server.Close()
	r, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	target, err := r.CreateTarget(context.Background(), domain.TargetInput{DisplayName: "SNMP load", BaseURL: server.URL + "/ext/mw", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.StartTrapLoad(context.Background(), target.ID, TrapLoadOptions{Port: 1162, Profile: profile, Rate: 2, Duration: 10, ClearDelay: 1, DrainTimeout: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.StartTrapLoad(context.Background(), target.ID, TrapLoadOptions{Port: 1162, Profile: profile, Rate: 2, Duration: 10, ClearDelay: 1, DrainTimeout: 10}); err == nil {
		t.Fatal("concurrent load accepted")
	}
	cancelled := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		got, _ := r.GetRun(context.Background(), run.ID)
		if got.Status != domain.RunCompleted {
			if mode == "cancel-clear" && !cancelled {
				if _, err := os.Stat(state + ".clear-started"); err == nil {
					if err := r.CancelRun(context.Background(), run.ID); err != nil {
						t.Fatal(err)
					}
					cancelled = true
				}
			}
			if mode == "cancel" && !cancelled && got.Status == domain.RunInjecting {
				events, _ := r.ListRunEvents(context.Background(), run.ID)
				for _, event := range events {
					if event.Type == "trap.result" && strings.Contains(event.DetailJSON, `"phase":"offer"`) {
						if err := r.CancelRun(context.Background(), run.ID); err != nil {
							t.Fatal(err)
						}
						cancelled = true
						break
					}
				}
			}
			continue
		}
		if expectedPass && (got.Verdict != domain.VerdictPass || got.CleanupStatus != domain.CleanupClean) {
			t.Fatalf("%s %s %s", got.Verdict, got.CleanupStatus, got.CanonicalReportJSON)
		}
		if strings.Contains(string(got.CanonicalReportJSON), "load-private") {
			t.Fatal("secret in report")
		}
		var report trapLoadReport
		if err := json.Unmarshal(got.CanonicalReportJSON, &report); err != nil {
			t.Fatal(err)
		}
		if !expectedPass && got.Verdict == domain.VerdictPass {
			t.Fatal("failed/cancelled run passed")
		}
		if mode == "mixed" && len(report.Preflight) != 4 {
			t.Fatalf("missing family preflight: %+v", report.Preflight)
		}
		if mode == "cancel-clear" {
			if !cancelled || got.CleanupStatus != domain.CleanupDirty || report.LocalFailures != 1 {
				t.Fatalf("ambiguous clear falsely clean: %+v", report)
			}
			clears := map[int]int{}
			for _, send := range report.Sends {
				if send.Kind == "clear" && send.Attempted {
					clears[send.Episode]++
					if clears[send.Episode] > 1 {
						t.Fatal("ambiguous clear blindly replayed")
					}
				}
			}
		}
		if mode == "missing-preflight" && (report.OfferAttempts != 0 || got.CleanupStatus != domain.CleanupUnknown) {
			t.Fatalf("unproven source: %+v", report)
		}
		if mode == "slow-sender" && (report.MaxLagMillis <= 1000 || report.UnattemptedPlanPackets == 0) {
			t.Fatalf("slow sender counted valid: %+v", report)
		}
		if mode == "cancel" && (!cancelled || report.UnattemptedPlanPackets == 0 || got.CleanupStatus != domain.CleanupClean) {
			t.Fatalf("cancel cleanup: %+v", report)
		}
		events, _ := r.ListRunEvents(context.Background(), run.ID)
		prepared, finished := 0, 0
		for _, e := range events {
			if e.Type == "alert.prepared" {
				prepared++
			}
			if e.Type == "trap.result" {
				finished++
				if finished > prepared {
					t.Fatal("send without durable preparation")
				}
			}
		}
		if prepared < 2 || prepared != finished || (mode == "complete" && prepared < 22) {
			t.Fatalf("ledger %d/%d", prepared, finished)
		}
		if _, err := r.RetryCleanup(context.Background(), run.ID); err == nil {
			t.Fatal("generic cleanup accepted SNMP source ownership")
		}
		destination := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := r.ExportRun(context.Background(), run.ID, destination); err != nil {
			t.Fatal(err)
		}
		if err := r.VerifyBundle(context.Background(), destination); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("load did not finish")
}
