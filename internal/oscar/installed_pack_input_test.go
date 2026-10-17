package oscar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func installedInputFixture(t *testing.T, origin string) (*testpack.Kit, domain.Target, Options) {
	t.Helper()
	kit, err := testpack.Load("../testpack/testdata/kit", "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59")
	if errors.Is(err, testpack.ErrUnsupportedPlatform) {
		t.Skip("installed kit descriptor loading is qualified only on Linux and macOS")
	}
	if err != nil {
		t.Fatal(err)
	}
	target := domain.Target{ID: "receiver", BaseURL: origin, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "RECEIVER_KEY"}}
	return kit, target, Options{Getenv: func(string) string { return "receiver-secret" }, Now: func() time.Time { return time.Date(2026, 10, 17, 12, 0, 1, 0, time.UTC) }}
}

func TestInstalledPackInputFrozenFifteenAndRecordedSend(t *testing.T) {
	var retained InstalledPackInputEvidence
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		frozen, _ := base64.StdEncoding.DecodeString(retained.Request.BodyBase64)
		if string(body) != string(frozen) || r.Method != "POST" || r.URL.Path != "/api/v1/webhookreceiver/webhook/corrtest_operational_v1" || r.Header.Get("If-Match") != retained.Request.ProfilePrecondition || r.Header.Get("X-API-Key") != "receiver-secret" {
			t.Error("wrong or unrecorded request")
		}
		w.Header().Set("X-Oscar-Profile-Revision", strings.Trim(retained.Request.ProfilePrecondition, `"`))
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	kit, target, options := installedInputFixture(t, server.URL)
	start := options.Now().Add(-time.Second)
	input, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), start, target, options)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]int{}
	sources := map[string]bool{}
	for index := 0; index < 15; index++ {
		e, body, err := input.Prepare(index)
		if err != nil {
			t.Fatal(err)
		}
		groups[e.Expected.Labels["site"]]++
		source := e.Expected.Labels["source_id"]
		if sources[source] || len(e.Expected.Labels) != 6 || !e.Expected.StartsAt.Equal(start) {
			t.Fatal("identity collision or changed label contract")
		}
		sources[source] = true
		var payload struct {
			Labels           map[string]string
			StartsAt, EndsAt time.Time
		}
		if json.Unmarshal(body, &payload) != nil || !payload.StartsAt.Equal(start) || payload.EndsAt.Sub(start) != 600*time.Second {
			t.Fatal("wrong finite firing interval")
		}
		result, err := input.SendRecorded(context.Background(), index, func(e InstalledPackInputEvidence) error {
			retained = e
			encoded, _ := json.Marshal(e)
			if strings.Contains(string(encoded), "receiver-secret") {
				t.Fatal("secret in evidence")
			}
			e.Expected.Labels["alertname"] = "caller mutation"
			return nil
		})
		if err != nil || result.Class != InjectionForwarded || len(result.ProfileRevisions) != 1 || result.ProfileRevisions[0] != kit.Bindings().ProfileEffectiveSHA256 {
			t.Fatalf("%d: %+v %v", index, result, err)
		}
	}
	if groups["positive"] != 5 || groups["negative"] != 4 || groups["isolated-a"] != 3 || groups["isolated-b"] != 3 || calls.Load() != 15 {
		t.Fatalf("groups=%v calls=%d", groups, calls.Load())
	}
	if _, err := input.SendRecorded(context.Background(), 0, func(InstalledPackInputEvidence) error { return nil }); err == nil || calls.Load() != 15 {
		t.Fatal("resent completed input")
	}
}

func TestInstalledPackInputStopsOnUncertainty(t *testing.T) {
	for _, mode := range []string{"record failure", "cancel after record", "expired", "future", "wrong revision", "missing revision", "duplicate revision", "wrong status", "duplicate status", "unicode alias", "null status", "extra body", "redirect", "profile changed", "lost response"} {
		t.Run(mode, func(t *testing.T) {
			var calls, records atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Oscar-Profile-Revision", "725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb")
				switch mode {
				case "wrong revision":
					w.Header().Set("X-Oscar-Profile-Revision", strings.Repeat("0", 64))
				case "missing revision":
					w.Header().Del("X-Oscar-Profile-Revision")
				case "duplicate revision":
					w.Header().Add("X-Oscar-Profile-Revision", strings.Repeat("0", 64))
				case "redirect":
					w.Header().Set("Location", r.URL.Path)
					w.WriteHeader(307)
					return
				case "profile changed":
					w.WriteHeader(412)
					return
				case "lost response":
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close()
					return
				}
				body := `{"status":"ok"}`
				switch mode {
				case "wrong status":
					body = `{"status":"acl_denied"}`
				case "duplicate status":
					body = `{"status":"acl_denied","status":"ok"}`
				case "unicode alias":
					body = `{"status":"acl_denied","ſtatus":"ok"}`
				case "null status":
					body = `{"status":null}`
				case "extra body":
					body += `{}`
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			kit, target, options := installedInputFixture(t, server.URL)
			start := options.Now().Add(-time.Second)
			if mode == "expired" {
				start = start.Add(-time.Minute)
			}
			if mode == "future" {
				start = start.Add(time.Minute)
			}
			input, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), start, target, options)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result, err := input.SendRecorded(ctx, 0, func(InstalledPackInputEvidence) error {
				records.Add(1)
				if mode == "record failure" {
					return errors.New("disk failure")
				}
				if mode == "cancel after record" {
					cancel()
				}
				return nil
			})
			if err == nil || result.Class != InjectionIndeterminate {
				t.Fatalf("uncertainty became success: %+v %v", result, err)
			}
			want := int32(1)
			if mode == "record failure" || mode == "cancel after record" || mode == "expired" || mode == "future" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("calls=%d want=%d", calls.Load(), want)
			}
			before := records.Load()
			for _, index := range []int{0, 1} {
				_, _ = input.SendRecorded(context.Background(), index, func(InstalledPackInputEvidence) error { records.Add(1); return nil })
			}
			if calls.Load() != want || records.Load() != before {
				t.Fatal("uncertain run continued or retried")
			}
		})
	}
}

func TestInstalledPackInputWindowCannotAdvanceDuringRecordingOrReply(t *testing.T) {
	for _, stage := range []string{"record", "reply"} {
		t.Run(stage, func(t *testing.T) {
			var offset, calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				offset.Store(61)
				w.Header().Set("X-Oscar-Profile-Revision", "725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb")
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}))
			defer server.Close()
			kit, target, options := installedInputFixture(t, server.URL)
			start := options.Now().Add(-time.Second)
			offset.Store(1)
			options.Now = func() time.Time { return start.Add(time.Duration(offset.Load()) * time.Second) }
			input, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), start, target, options)
			if err != nil {
				t.Fatal(err)
			}
			result, err := input.SendRecorded(context.Background(), 0, func(InstalledPackInputEvidence) error {
				if stage == "record" {
					offset.Store(61)
				}
				return nil
			})
			want := int64(1)
			if stage == "record" {
				want = 0
			}
			if err == nil || result.Class != InjectionIndeterminate || calls.Load() != want {
				t.Fatalf("late operation: %+v err=%v calls=%d", result, err, calls.Load())
			}
		})
	}
}

func TestInstalledPackInputRejectsInvalidConstruction(t *testing.T) {
	for _, mode := range []string{"nil kit", "empty kit", "run", "subsecond", "offset", "zero time", "missing id", "no credential", "empty credential", "insecure", "custom client", "input override", "ordinary binding", "origin path", "origin query", "origin fragment", "profile"} {
		t.Run(mode, func(t *testing.T) {
			kit, target, options := installedInputFixture(t, "http://127.0.0.1:1")
			runID, start := strings.Repeat("a", 32), options.Now()
			switch mode {
			case "nil kit":
				kit = nil
			case "empty kit":
				kit = &testpack.Kit{}
			case "run":
				runID = "not-fresh"
			case "subsecond":
				start = start.Add(time.Nanosecond)
			case "offset":
				start = start.In(time.FixedZone("non-UTC", 3600))
			case "zero time":
				start = time.Time{}
			case "missing id":
				target.ID = ""
			case "no credential":
				target.Credential = domain.CredentialRef{}
			case "empty credential":
				options.Getenv = func(string) string { return " " }
			case "insecure":
				target.TLS.Insecure = true
			case "custom client":
				options.HTTPClient = &http.Client{}
			case "input override":
				options.InputPath = "alert-engine"
			case "ordinary binding":
				target.OrdinaryWebhook = &domain.OrdinaryWebhookBinding{}
			case "origin path":
				target.BaseURL += "/api"
			case "origin query":
				target.BaseURL += "?"
			case "origin fragment":
				target.BaseURL += "#"
			case "profile":
				target.APIProfile = "legacy"
			}
			if _, err := NewInstalledPackInput(kit, runID, start, target, options); err == nil {
				t.Fatal("invalid construction accepted")
			}
		})
	}
}

func TestInstalledPackInputConcurrentDuplicateSendsOnce(t *testing.T) {
	var calls, records atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Oscar-Profile-Revision", "725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	kit, target, options := installedInputFixture(t, server.URL)
	input, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), options.Now().Add(-time.Second), target, options)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = input.SendRecorded(context.Background(), 0, func(InstalledPackInputEvidence) error { records.Add(1); return nil })
		}()
	}
	group.Wait()
	if calls.Load() != 1 || records.Load() != 1 {
		t.Fatalf("calls=%d records=%d", calls.Load(), records.Load())
	}
}
