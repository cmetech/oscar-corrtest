package runner_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

type thresholdObservation struct{ Trigger, Count, Distinct int }

func thresholdDocument() scenario.Scenario {
	doc := scenario.Builtin("threshold")
	for i := range doc.Cases {
		item := &doc.Cases[i]
		item.Events = nil
		count := 4
		if i == 1 {
			count = 3
		}
		for n := 0; n < count; n++ {
			value := fmt.Sprintf("device-%d", n+1)
			if i == 1 {
				value = "device-1"
			} else if n == 3 {
				value = "device-3"
			}
			item.Events = append(item.Events, scenario.Event{Role: "cpu_high", Source: string(rune('A' + n)), Status: "firing", Delay: time.Duration(n) * 3 * time.Second, Labels: map[string]string{"device": value}})
		}
		for n := 0; n < count; n++ {
			item.Events = append(item.Events, scenario.Event{Role: "cpu_high", Source: string(rune('A' + n)), Status: "resolved", Delay: time.Duration(count+n) * 3 * time.Second})
		}
		expected := 0
		if i == 0 {
			expected = 1
		}
		item.Checkpoints = []scenario.Checkpoint{
			{Name: "two-inputs-no-trigger", AfterEvent: 2, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 0}}},
			{Name: "third-input-decision", AfterEvent: 3, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: expected}}},
		}
		if i == 0 {
			item.Checkpoints = append(item.Checkpoints, scenario.Checkpoint{Name: "duplicate-no-second-trigger", AfterEvent: 4, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 1}}})
		}
		item.Assertions = []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: count}}
	}
	return doc
}

func runThresholdCase(t *testing.T, index int, oracle func(compiler.RulePlan, oscar.AlertRequest, time.Time) (thresholdObservation, error), want domain.Verdict) *correlationWireFixture {
	t.Helper()
	db := openDatabase(t)
	run := newRun()
	ctx := context.Background()
	if err := db.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, thresholdDocument(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	plan.Cases = plan.Cases[index : index+1]
	plan.MutationBudget.Rules = 1
	plan.MutationBudget.Alerts = len(plan.Cases[0].Alerts)
	fixture := newCorrelationWireFixture("valid")
	fixture.thresholdOracle = oracle
	fixture.model = testoscar.NewModel(time.Date(2026, 10, 15, 12, 0, 1, 0, time.UTC))
	server := testoscar.NewWithResponder(t, fixture.respond)
	client, err := oscar.New(domain.Target{BaseURL: server.URL(), APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "local-test-key" }})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.New(db, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
	if (err != nil) != (want == domain.VerdictError) {
		t.Fatalf("execution error=%v want=%s", err, want)
	}
	stored, err := db.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != want {
		t.Fatalf("verdict=%s want=%s report=%s", stored.Verdict, want, stored.CanonicalReportJSON)
	}
	var report struct{ Coverage map[string]string }
	if err := json.Unmarshal(stored.CanonicalReportJSON, &report); err != nil {
		t.Fatal(err)
	}
	if report.Coverage["physicalClear"] != "NOT_PROVEN" || report.Coverage["receiverAdmission"] != "NOT_TESTED" || report.Coverage["nativeAlertmanager"] != "BYPASSED" {
		t.Fatal("overclaimed coverage")
	}
	return fixture
}

func TestThresholdOracleOverridesExactSetPositive(t *testing.T) {
	calls := 0
	fixture := runThresholdCase(t, 0, func(compiler.RulePlan, oscar.AlertRequest, time.Time) (thresholdObservation, error) {
		calls++
		return thresholdObservation{Count: calls, Distinct: 1}, nil
	}, domain.VerdictFail)
	if calls != 3 || len(fixture.stimuli) != 3 {
		t.Fatalf("calls=%d stimuli=%d", calls, len(fixture.stimuli))
	}
}

func TestThresholdOracleFailureStopsLaterStimuli(t *testing.T) {
	calls := 0
	runThresholdCase(t, 0, func(compiler.RulePlan, oscar.AlertRequest, time.Time) (thresholdObservation, error) {
		calls++
		return thresholdObservation{}, fmt.Errorf("oracle unavailable")
	}, domain.VerdictError)
	if calls != 1 {
		t.Fatalf("oracle calls=%d", calls)
	}
}

func parseThresholdObservation(raw []byte) (thresholdObservation, error) {
	var values []json.RawMessage
	if len(raw) > 1024 || json.Unmarshal(raw, &values) != nil || len(values) != 3 {
		return thresholdObservation{}, fmt.Errorf("Threshold oracle returned invalid evidence")
	}
	var tuple [3]int
	for i, value := range values {
		number, err := strconv.Atoi(string(value))
		if err != nil {
			return thresholdObservation{}, fmt.Errorf("Threshold oracle returned noninteger evidence")
		}
		tuple[i] = number
	}
	if tuple[0] < 0 || tuple[0] > 1 || tuple[1] < 1 || tuple[2] < 0 || (tuple[0] == 1 && tuple[2] < 3) {
		return thresholdObservation{}, fmt.Errorf("Threshold oracle returned invalid counts")
	}
	return thresholdObservation{tuple[0], tuple[1], tuple[2]}, nil
}

func TestThresholdOracleMalformedReplyRefused(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "[1,3]", "[1,3,3,0]", "[2,3,3]", "[1,-1,3]", "[1,3,2]", "[0,1,null]", "[null,1,1]", "[0,null,1]", "[0,1,1.5]", "[0,1,1] trailing", `"ERR unavailable"`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseThresholdObservation([]byte(raw)); err == nil {
				t.Fatalf("accepted malformed evidence %q", raw)
			}
		})
	}
}

// The parent owns the supplied CLI target. This adapter never starts services or
// flushes a database; each invocation has fresh keys and exact member cleanup.
type thresholdRedisOracle struct {
	script        []byte
	argv          []string
	prefix        string
	keys, windows map[string]bool
	observations  []thresholdObservation
	buckets       []int64
}

func loadThresholdRedis(scriptPath, expectedHash, commandJSON string) (*thresholdRedisOracle, error) {
	var argv []string
	if !filepath.IsAbs(scriptPath) || len(expectedHash) != 64 || json.Unmarshal([]byte(commandJSON), &argv) != nil || len(argv) < 1 || len(argv) > 24 || !filepath.IsAbs(argv[0]) {
		return nil, fmt.Errorf("explicit Threshold Lua path/hash and CLI argv required")
	}
	for _, arg := range argv {
		if arg == "" || len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return nil, fmt.Errorf("invalid Threshold CLI argv")
		}
	}
	info, err := os.Stat(scriptPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 131072 {
		return nil, fmt.Errorf("bounded regular Threshold Lua file required")
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(script)
	if hex.EncodeToString(hash[:]) != expectedHash {
		return nil, fmt.Errorf("Threshold Lua hash differs")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &thresholdRedisOracle{script: script, argv: argv, prefix: "corrtest:threshold:" + hex.EncodeToString(nonce[:]), keys: map[string]bool{}, windows: map[string]bool{}}, nil
}

func (o *thresholdRedisOracle) command(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argv := append(append([]string{}, o.argv[1:]...), args...)
	raw, err := exec.CommandContext(ctx, o.argv[0], argv...).Output()
	if err != nil {
		return nil, fmt.Errorf("Threshold Redis CLI unavailable: %w", err)
	}
	if len(raw) > 65536 {
		return nil, fmt.Errorf("Threshold CLI response bound exceeded")
	}
	return raw, nil
}

func (o *thresholdRedisOracle) observe(rule compiler.RulePlan, alert oscar.AlertRequest, at time.Time) (thresholdObservation, error) {
	if rule.Pattern != "threshold" || rule.WindowSeconds != 30 || alert.Status != "firing" || alert.Fingerprint == "" || alert.Labels["device"] == "" {
		return thresholdObservation{}, fmt.Errorf("finite Threshold oracle input required")
	}
	minValid := false
	switch minimum := rule.MatchCriteria["min_distinct_count"].(type) {
	case int:
		minValid = minimum == 3
	case float64:
		minValid = minimum == 3
	}
	if !minValid || rule.MatchCriteria["distinct_label"] != "device" {
		return thresholdObservation{}, fmt.Errorf("fixed three-device Threshold contract required")
	}
	group := []string{rule.Name}
	for _, key := range rule.GroupBy {
		group = append(group, key, alert.Labels[key])
	}
	encoded, _ := json.Marshal(group)
	hash := sha256.Sum256(encoded)
	suffix := hex.EncodeToString(hash[:])
	bucket := at.UnixMilli() / 30000
	win, hll, fired := o.prefix+":win:"+suffix, o.prefix+":hll:"+suffix+":"+strconv.FormatInt(bucket, 10), o.prefix+":fired:"+suffix
	for _, key := range []string{win, hll, fired} {
		o.keys[key] = true
	}
	o.windows[win] = true
	raw, err := o.command("EVAL", string(o.script), "3", win, hll, fired, alert.Fingerprint, strconv.FormatInt(at.UnixMilli(), 10), strconv.FormatInt(at.UnixMilli()-30000, 10), alert.Labels["device"], "3", "60", "30")
	if err != nil {
		return thresholdObservation{}, err
	}
	observation, err := parseThresholdObservation(raw)
	if err == nil {
		o.observations = append(o.observations, observation)
		o.buckets = append(o.buckets, bucket)
	}
	return observation, err
}

func (o *thresholdRedisOracle) close() error {
	for window := range o.windows {
		raw, err := o.command("ZREM", "oscar:correlator:active_windows", window)
		if err != nil {
			return err
		}
		if count, err := strconv.Atoi(strings.TrimSpace(string(raw))); err != nil || count < 0 || count > 1 {
			return fmt.Errorf("Threshold active-window cleanup unproved")
		}
	}
	for key := range o.keys {
		raw, err := o.command("DEL", key)
		if err != nil {
			return err
		}
		if count, err := strconv.Atoi(strings.TrimSpace(string(raw))); err != nil || count < 0 || count > 1 {
			return fmt.Errorf("Threshold key cleanup unproved")
		}
		raw, err = o.command("EXISTS", key)
		if err != nil || strings.TrimSpace(string(raw)) != "0" {
			return fmt.Errorf("Threshold key absence unproved")
		}
	}
	return nil
}

func TestThresholdRedisConfigurationRefusesIncompleteBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threshold.lua")
	script := []byte("return {0,1,1}")
	if err := os.WriteFile(path, script, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(script)
	expected := hex.EncodeToString(hash[:])
	for _, tc := range []struct{ name, path, hash, argv string }{
		{"missing path", "", expected, `["/unused/redis-cli","--json"]`},
		{"wrong hash", path, strings.Repeat("0", 64), `["/unused/redis-cli","--json"]`},
		{"shell text", path, expected, `"redis-cli --json"`},
		{"empty argv", path, expected, `[]`},
		{"relative executable", path, expected, `["redis-cli"]`},
		{"null argument", path, expected, `["/unused/redis-cli",null]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadThresholdRedis(tc.path, tc.hash, tc.argv); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	oracle, err := loadThresholdRedis(path, expected, `["/unused/redis-cli","--json"]`)
	if err != nil {
		t.Fatal(err)
	}
	if string(oracle.script) != string(script) || len(oracle.argv) != 2 {
		t.Fatal("frozen input changed")
	}
}

func TestThresholdOracleMalformedEvidenceStopsRunner(t *testing.T) {
	calls := 0
	runThresholdCase(t, 0, func(compiler.RulePlan, oscar.AlertRequest, time.Time) (thresholdObservation, error) {
		calls++
		return parseThresholdObservation([]byte(`[1,3]`))
	}, domain.VerdictError)
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestThresholdRedisOracleThroughRealHTTPClient(t *testing.T) {
	if os.Getenv("CORRTEST_THRESHOLD_REDIS") == "" {
		t.Skip("root-owned Redis qualification is not enabled")
	}
	if os.Getenv("CORRTEST_THRESHOLD_REDIS") != "1" {
		t.Fatal("explicit CORRTEST_THRESHOLD_REDIS=1 required")
	}
	path, hash := os.Getenv("CORRTEST_THRESHOLD_LUA"), os.Getenv("CORRTEST_THRESHOLD_LUA_SHA256")
	oracle, err := loadThresholdRedis(path, hash, os.Getenv("CORRTEST_THRESHOLD_REDIS_ARGV"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := oracle.close(); err != nil {
			t.Error(err)
		} else {
			t.Logf("THRESHOLD_REDIS_CLEANUP keys=%d windows=%d absent=true", len(oracle.keys), len(oracle.windows))
		}
	})
	t.Cleanup(func() {
		raw, err := os.ReadFile(path)
		got := sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(got[:]) != hash {
			t.Error("Lua source changed during qualification")
		}
	})
	for index := 0; index < 2; index++ {
		start := len(oracle.observations)
		fixture := runThresholdCase(t, index, oracle.observe, domain.VerdictPass)
		expected := []thresholdObservation{{0, 1, 1}, {0, 2, 2}, {1, 3, 3}, {0, 4, 3}}
		if index == 1 {
			expected = []thresholdObservation{{0, 1, 1}, {0, 2, 1}, {0, 3, 1}}
		}
		got := oracle.observations[start:]
		if fmt.Sprint(got) != fmt.Sprint(expected) {
			t.Fatalf("Redis observations=%v want=%v", got, expected)
		}
		for _, bucket := range oracle.buckets[start:] {
			if bucket != oracle.buckets[start] {
				t.Fatal("HTTP case crossed its fixed bucket")
			}
		}
		if len(fixture.cleanup) != len(expected) || len(fixture.deleted) != 1 {
			t.Fatal("HTTP fixture cleanup incomplete")
		}
		t.Logf("THRESHOLD_REDIS_HTTP case=%d observations=%v bucket=%d modeled_History=true", index, got, oracle.buckets[start])
	}
	rule := compiler.RulePlan{Name: "separate-group-control", Pattern: "threshold", WindowSeconds: 30, GroupBy: []string{"site"}, MatchCriteria: map[string]any{"distinct_label": "device", "min_distinct_count": 3}}
	start := time.Date(2026, 10, 15, 12, 0, 1, 0, time.UTC)
	for i, site := range []string{"A", "A", "B"} {
		got, err := oracle.observe(rule, oscar.AlertRequest{Fingerprint: fmt.Sprint(i), Status: "firing", Labels: map[string]string{"site": site, "device": fmt.Sprint(i)}}, start.Add(time.Duration(i)*time.Second))
		if err != nil || got.Trigger != 0 || got.Distinct != []int{1, 2, 1}[i] {
			t.Fatalf("separate group=%v err=%v", got, err)
		}
		t.Logf("THRESHOLD_REDIS_GROUP site=%s observed=%v", site, got)
	}
	rule.Name = "boundary-control"
	for i, offset := range []int{28, 30, 31} {
		got, err := oracle.observe(rule, oscar.AlertRequest{Fingerprint: fmt.Sprint(i), Status: "firing", Labels: map[string]string{"site": "A", "device": fmt.Sprint(i)}}, start.Add(time.Duration(offset)*time.Second))
		if err != nil || got.Trigger != 0 || got.Distinct != []int{1, 1, 2}[i] {
			t.Fatalf("bucket boundary=%v err=%v", got, err)
		}
		t.Logf("THRESHOLD_REDIS_BOUNDARY offset=%d observed=%v", offset, got)
	}
	t.Logf("THRESHOLD_REDIS_ORACLE_PASS lua_sha256=%s group_and_bucket_isolation=true Native=BYPASSED History=MODELED UAT=0", hash)
}

func TestThresholdCLIArgvHelper(t *testing.T) {
	if os.Getenv("CORRTEST_THRESHOLD_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := json.NewEncoder(os.Stdout).Encode(os.Args[i+1:]); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestThresholdCLIUsesLiteralArgv(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORRTEST_THRESHOLD_CLI_HELPER", "1")
	oracle := thresholdRedisOracle{argv: []string{executable, "-test.run=^TestThresholdCLIArgvHelper$", "--"}}
	values := []string{"EVAL", "return {'literal $HOME `touch nothing` ; $(false)'}", "3", "key with spaces"}
	raw, err := oracle.command(values...)
	if err != nil {
		t.Fatal(err)
	}
	var received []string
	if json.Unmarshal(raw, &received) != nil || fmt.Sprint(received) != fmt.Sprint(values) {
		t.Fatalf("argv changed: %q", raw)
	}
}

func TestThresholdOracleValidReplies(t *testing.T) {
	for _, raw := range []string{"[0,1,1]", "[1,3,3]", "[0,4,3]"} {
		if _, err := parseThresholdObservation([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}
