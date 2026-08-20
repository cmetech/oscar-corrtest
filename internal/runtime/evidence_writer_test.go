package runtime

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestEvidenceWriterPublishesVerifiedNormalizedFacts(t *testing.T) {
	state := t.TempDir()
	runtime, err := Open(context.Background(), config.Settings{DataDir: state, ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	run, err := runtime.CreateRun(context.Background(), "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	facts := domain.ExecutionFacts{Cases: []domain.CaseFact{{StableKey: "P01", Verdict: domain.VerdictPass,
		StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), Evidence: []byte(`{"history":[{"fingerprint":"server-fp"}],"note":"not-SENTINEL_SECRET"}`)}}}
	artifact, err := runtime.evidenceWriter().WriteEvidence(context.Background(), run.ID, facts, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	items, err := runtime.ListArtifactEvidence(context.Background(), run.ID)
	if err != nil || len(items) != 1 || items[0].Integrity != domain.ArtifactIntegrityValid || artifact.Availability != domain.ArtifactAvailable {
		t.Fatalf("artifact=%+v evidence=%+v err=%v", artifact, items, err)
	}
	content, err := os.ReadFile(state + "/" + artifact.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "server-fp") || strings.Contains(string(content), "credential") {
		t.Fatalf("normalized evidence=%s", content)
	}
}

func TestCompletedRunHasVerifiedNormalizedEvidenceArtifact(t *testing.T) {
	state := t.TempDir()
	runtime, err := Open(context.Background(), config.Settings{DataDir: state, ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	run, err := runtime.CreateRun(context.Background(), "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	model := testoscar.NewModel(time.Date(2026, 8, 20, 13, 0, 0, 0, time.UTC))
	engine := runner.New(runtime.database, model, runner.Options{Now: model.Now, Sleep: model.Sleep, PollInterval: time.Second, Stabilization: time.Second, EvidenceWriter: runtime.evidenceWriter()})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	items, err := runtime.ListArtifactEvidence(context.Background(), run.ID)
	if err != nil || len(items) != 1 || items[0].Integrity != domain.ArtifactIntegrityValid || items[0].Artifact.Kind != "normalized-oscar-evidence" {
		t.Fatalf("artifacts=%+v err=%v", items, err)
	}
	stored, err := runtime.GetRun(context.Background(), run.ID)
	if err != nil || stored.Verdict != domain.VerdictPass || !strings.Contains(string(stored.CanonicalReportJSON), `"artifacts":[`) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}
