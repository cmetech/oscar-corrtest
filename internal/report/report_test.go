package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestBuildProducesDeterministicSecretFreeCanonicalJSON(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	input := Input{
		HarnessVersion: "v0.1.0",
		Target: domain.Target{
			ID: "tgt_a", DisplayName: "Lab A", BaseURL: "https://oscar.example/api", APIProfile: "public-v1",
			Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "SENTINEL_SECRET_VALUE"},
		},
		Run: domain.Run{
			ID: "crt_00000000000000000000000000", ShortToken: "00000000", Status: domain.RunCompleted,
			Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupDirty, HarnessVersion: "v0.1.0", CreatedAt: now, UpdatedAt: now,
		},
		Events: []domain.RunEvent{
			{RunID: "crt_00000000000000000000000000", Sequence: 2, Type: "second", Level: "info", OccurredAt: now, Summary: "second"},
			{RunID: "crt_00000000000000000000000000", Sequence: 1, Type: "first", Level: "info", OccurredAt: now, Summary: "first"},
		},
		Artifacts: []ArtifactEvidence{
			{Manifest: artifact.Manifest{RelativePath: "runs/x/z.json", MIMEType: "application/json", SHA256: "b", ByteSize: 2}, Integrity: artifact.IntegrityMissing},
			{Manifest: artifact.Manifest{RelativePath: "runs/x/a.json", MIMEType: "application/json", SHA256: "a", ByteSize: 1}, Integrity: artifact.IntegrityValid},
		},
	}
	first, err := Marshal(Build(input))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build(input))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("report bytes differ\n%s\n%s", first, second)
	}
	text := string(first)
	for _, required := range []string{
		`"apiVersion":"corrtest.oscar/v1alpha1"`, `"status":"COMPLETED"`, `"verdict":"PASS"`,
		`"cleanupStatus":"DIRTY"`, `"integrity":"missing"`, `"type":"first"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("report missing %s: %s", required, text)
		}
	}
	if strings.Contains(text, "SENTINEL_SECRET_VALUE") || strings.Contains(text, "credential") {
		t.Fatalf("report leaked credential metadata: %s", text)
	}
	if strings.Index(text, `"type":"first"`) > strings.Index(text, `"type":"second"`) {
		t.Fatalf("events not sorted: %s", text)
	}
}
