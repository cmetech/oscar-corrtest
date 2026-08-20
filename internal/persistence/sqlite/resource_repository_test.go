package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestResourceLifecyclePreservesOwnershipAndCleanupEvidence(t *testing.T) {
	database := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000042", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	run.Status = domain.RunSettingUp
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	resource := domain.Resource{ID: "res_1", RunID: run.ID, Kind: "correlation_rule", ExternalName: "corrtest-flood-p01-00000042",
		OwnershipToken: run.ID, LifecycleState: domain.ResourceProposed}
	if err := database.CreateResource(context.Background(), resource); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	if err := database.AdoptResource(context.Background(), resource.ID, "71", createdAt); err != nil {
		t.Fatal(err)
	}
	resources, err := database.ListResources(context.Background(), run.ID)
	if err != nil || len(resources) != 1 || resources[0].ExternalID != "71" || resources[0].LifecycleState != domain.ResourceCreated {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
	deletedAt := createdAt.Add(time.Minute)
	if err := database.MarkResourceDeleted(context.Background(), resource.ID, deletedAt); err != nil {
		t.Fatal(err)
	}
	resources, err = database.ListResources(context.Background(), run.ID)
	if err != nil || resources[0].DeletedAt == nil || resources[0].LifecycleState != domain.ResourceDeleted {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
}

func TestResourceCannotBeReownedOrBlindlyAdopted(t *testing.T) {
	database := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000043", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	run.Status = domain.RunSettingUp
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	resource := domain.Resource{ID: "res_2", RunID: run.ID, Kind: "correlation_rule", ExternalName: "corrtest-flood-p01-00000043",
		OwnershipToken: run.ID, LifecycleState: domain.ResourceProposed}
	if err := database.CreateResource(context.Background(), resource); err != nil {
		t.Fatal(err)
	}
	if err := database.AdoptResource(context.Background(), resource.ID, "", time.Now()); err == nil {
		t.Fatal("empty external id adopted")
	}
	if err := database.CreateResource(context.Background(), domain.Resource{ID: "res_3", RunID: run.ID, Kind: "correlation_rule", ExternalName: resource.ExternalName,
		OwnershipToken: "crt_attacker", LifecycleState: domain.ResourceProposed}); err == nil {
		t.Fatal("duplicate external name accepted")
	}
}

func TestUnknownResourceCanBeAdoptedAfterExactRecoveryProof(t *testing.T) {
	database := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000044", time.Now().UTC())
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	resource := domain.Resource{ID: "res_unknown", RunID: run.ID, Kind: "correlation_rule", ExternalName: "owned-rule", OwnershipToken: run.ID, LifecycleState: domain.ResourceUnknown}
	if err := database.CreateResource(context.Background(), resource); err != nil {
		t.Fatal(err)
	}
	if err := database.AdoptResource(context.Background(), resource.ID, "71", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, err := database.ListResources(context.Background(), run.ID)
	if err != nil || len(items) != 1 || items[0].LifecycleState != domain.ResourceCreated || items[0].ExternalID != "71" {
		t.Fatalf("resources=%+v err=%v", items, err)
	}
}
