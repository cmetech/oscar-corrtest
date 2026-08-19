package history

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
)

func TestCreateTargetAppliesDefaultsAndPersists(t *testing.T) {
	database, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	service := New(database, func() time.Time { return now }, bytes.NewReader(make([]byte, 16)))

	target, err := service.CreateTarget(context.Background(), domain.TargetInput{
		DisplayName: "Lab A",
		BaseURL:     "https://oscar.example/",
		Credential:  domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OSCAR_API_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.APIProfile != "public-v1" || target.CreatedAt != now || target.UpdatedAt != now {
		t.Fatalf("target=%+v", target)
	}
	if target.ID == "" {
		t.Fatal("empty target ID")
	}
	list, err := service.ListTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != target.ID {
		t.Fatalf("targets=%+v", list)
	}
}
