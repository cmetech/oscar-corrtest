package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestTargetRepositoryPersistsSanitizedMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrtest.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	target := domain.Target{
		ID:          "tgt_01KTEST",
		DisplayName: "Lab A",
		BaseURL:     "https://oscar.example/api",
		APIProfile:  "public-v1",
		Credential:  domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OSCAR_API_TOKEN"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := database.CreateTarget(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	got, err := database.GetTarget(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("target=%+v want=%+v", got, target)
	}
	list, err := database.ListTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != target {
		t.Fatalf("targets=%+v", list)
	}
}

func TestTargetRepositoryEnforcesCaseInsensitiveNames(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	first := domain.Target{ID: "tgt_one", DisplayName: "Lab A", BaseURL: "https://one.example", APIProfile: "public-v1", CreatedAt: now, UpdatedAt: now}
	second := domain.Target{ID: "tgt_two", DisplayName: "lab a", BaseURL: "https://two.example", APIProfile: "public-v1", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateTarget(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateTarget(context.Background(), second); err == nil {
		t.Fatal("duplicate name was accepted")
	}
}
