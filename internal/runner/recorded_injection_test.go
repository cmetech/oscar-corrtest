package runner_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestRunnerRecordsExactBodyBeforeAmbiguousHTTP(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "stored then 503", true: "record fails"}[fail], func(t *testing.T) {
			db := openDatabase(t)
			ctx := context.Background()
			run := newRun()
			if err := db.CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			doc := scenario.Builtin("flood")
			doc.Cases[0].Role = ""
			doc.Cases[0].Repeat = 0
			for _, name := range []string{"A", "B", "C", "D", "E"} {
				doc.Cases[0].Events = append(doc.Cases[0].Events, scenario.Event{Role: "interface_down", Source: name, Status: "firing"})
			}
			plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			fixture := newCorrelationWireFixture("stored first 503")
			posted := 0
			var committed oscar.InjectionRequestEvidence
			server := testoscar.NewWithResponder(t, func(req testoscar.Request) testoscar.Response {
				if req.Method == http.MethodPost && req.Path == "/api/v1/alerts" && !strings.Contains(req.Body, "oscar_test_cleanup") {
					posted++
					events, err := db.ListRunEvents(ctx, run.ID)
					if err != nil {
						t.Error(err)
					}
					for _, event := range events {
						if event.Type == "alert.attempting" {
							var fact struct {
								Request oscar.InjectionRequestEvidence
							}
							if err := json.Unmarshal([]byte(event.DetailJSON), &fact); err != nil {
								t.Error(err)
							}
							committed = fact.Request
						}
					}
					body, err := base64.StdEncoding.DecodeString(committed.BodyBase64)
					sum := sha256.Sum256([]byte(req.Body))
					if err != nil || string(body) != req.Body || committed.BodySHA256 != hex.EncodeToString(sum[:]) {
						t.Error("actual POST preceded exact committed request evidence")
					}
				}
				return fixture.respond(req)
			})
			client, err := oscar.New(domain.Target{ID: "recorded-target", BaseURL: server.URL(), APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "never-persist-this-credential" }})
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(attemptStore{Store: db, fail: fail}, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if err == nil {
				t.Fatal("expected record or ambiguous HTTP error")
			}
			wantPosts := 1
			if fail {
				wantPosts = 0
			}
			if posted != wantPosts {
				t.Fatalf("POST count=%d want=%d", posted, wantPosts)
			}
			if fail {
				return
			}
			if committed.Endpoint != server.URL()+"/api/v1/alerts" || committed.TargetID != "recorded-target" {
				t.Fatal("target binding missing")
			}
			saved, err := db.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var report struct{ Attempts []domain.AlertAttemptFact }
			if err := json.Unmarshal(saved.CanonicalReportJSON, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Attempts) != 1 || report.Attempts[0].SendState != "ATTEMPTED" || report.Attempts[0].StatusCode != 503 {
				t.Fatal("ambiguous attempt was laundered")
			}
			events, err := db.ListRunEvents(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := evidence.Write(ctx, filepath.Join(t.TempDir(), "run.zip"), saved, events)
			if err != nil {
				t.Fatal(err)
			}
			archive, err := zip.OpenReader(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			found := false
			for _, file := range archive.File {
				stream, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(stream)
				_ = stream.Close()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(data, []byte("never-persist-this-credential")) || bytes.Contains(data, []byte("X-API-Key")) {
					t.Fatalf("credential leaked into %s", file.Name)
				}
				if file.Name == "events.json" {
					found = bytes.Contains(data, []byte(committed.BodyBase64)) && bytes.Contains(data, []byte(committed.BodySHA256))
				}
			}
			if !found {
				t.Fatal("exact prepared request missing from exported timeline")
			}
		})
	}
}

// Block after the real transaction commits and before the client can POST.
type crashAfterRequestStore struct{ runner.Store }

func (s crashAfterRequestStore) AppendRunEvent(ctx context.Context, event domain.RunEvent) (domain.RunEvent, error) {
	saved, err := s.Store.AppendRunEvent(ctx, event)
	if err == nil && event.Type == "alert.attempting" {
		fmt.Println("READY")
		select {}
	}
	return saved, err
}
func TestRecordedRequestSurvivesProcessLossBeforePOST(t *testing.T) {
	const helper = "CORRTEST_RECORDED_CRASH"
	ctx := context.Background()
	if os.Getenv(helper) == "1" {
		db, err := sqlite.Open(ctx, os.Getenv("CORRTEST_RECORDED_DB"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		run := newRun()
		if err := db.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		doc := scenario.Builtin("flood")
		doc.Cases[0].Role, doc.Cases[0].Repeat = "", 0
		for _, name := range []string{"A", "B", "C", "D", "E"} {
			doc.Cases[0].Events = append(doc.Cases[0].Events, scenario.Event{Role: "interface_down", Source: name, Status: "firing"})
		}
		plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
		if err != nil {
			t.Fatal(err)
		}
		client, err := oscar.New(domain.Target{ID: "crash-target", BaseURL: os.Getenv("CORRTEST_RECORDED_URL"), APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "crash-test-key" }})
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.New(crashAfterRequestStore{db}, client, runner.Options{}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
			t.Fatal(err)
		}
		t.Fatal("helper returned without blocking")
	}
	var posts atomic.Int32
	fixture := newCorrelationWireFixture("")
	server := testoscar.NewWithResponder(t, func(req testoscar.Request) testoscar.Response {
		if req.Method == "POST" && req.Path == "/api/v1/alerts" {
			posts.Add(1)
		}
		return fixture.respond(req)
	})
	path := filepath.Join(t.TempDir(), "corrtest.db")
	childCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestRecordedRequestSurvivesProcessLossBeforePOST$")
	command.Env = append(os.Environ(), helper+"=1", "CORRTEST_RECORDED_DB="+path, "CORRTEST_RECORDED_URL="+server.URL())
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "READY" {
		first := scanner.Text()
		var rest []string
		for scanner.Scan() {
			rest = append(rest, scanner.Text())
		}
		t.Fatalf("helper=%q rest=%q err=%v", first, rest, scanner.Err())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.RecoverInterruptedRuns(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetRun(ctx, newRun().ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.RunInterrupted || saved.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("status=%s cleanup=%s", saved.Status, saved.CleanupStatus)
	}
	events, err := db.ListRunEvents(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, event := range events {
		if event.Type != "alert.attempting" {
			continue
		}
		found++
		var fact struct {
			domain.AlertAttemptFact
			Request oscar.InjectionRequestEvidence
		}
		if err := json.Unmarshal([]byte(event.DetailJSON), &fact); err != nil {
			t.Fatal(err)
		}
		raw, err := base64.StdEncoding.DecodeString(fact.Request.BodyBase64)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		var payload oscar.AlertGroupRequest
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Alerts) != 1 || fact.StartsAt.IsZero() || payload.Alerts[0].StartsAt != fact.StartsAt.Format(time.RFC3339Nano) || fact.Request.Endpoint != server.URL()+"/api/v1/alerts" || fact.Request.TargetID != "crash-target" || fact.Request.BodyBytes != len(raw) || fact.Request.BodySHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("committed exact request was not retained")
		}
	}
	if found != 1 || posts.Load() != 0 {
		t.Fatalf("attempts=%d posts=%d", found, posts.Load())
	}
}
