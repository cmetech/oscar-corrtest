// Package history coordinates durable target and run-history operations.
package history

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type targetStore interface {
	CreateTarget(context.Context, domain.Target) error
	ListTargets(context.Context) ([]domain.Target, error)
	GetTarget(context.Context, string) (domain.Target, error)
}

type runStore interface {
	CreateRun(context.Context, domain.Run) error
	GetRun(context.Context, string) (domain.Run, error)
	ListRuns(context.Context, domain.RunFilter) ([]domain.Run, error)
	ListRunEvents(context.Context, string) ([]domain.RunEvent, error)
	RecoverInterruptedRuns(context.Context, time.Time) (int, error)
}

type store interface {
	targetStore
	runStore
}

// Service coordinates validation, identifiers, and timestamps around repositories.
type Service struct {
	store  store
	now    func() time.Time
	random io.Reader
}

// New constructs a durable history service.
func New(store store, now func() time.Time, random io.Reader) *Service {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	return &Service{store: store, now: now, random: random}
}

// CreateRun persists a queued run without starting target activity.
func (s *Service) CreateRun(ctx context.Context, targetID, scenarioID, harnessVersion string) (domain.Run, error) {
	id, err := domain.NewRunID(s.random)
	if err != nil {
		return domain.Run{}, fmt.Errorf("create run id: %w", err)
	}
	now := s.now().UTC()
	run := domain.Run{
		ID:             id.String(),
		ShortToken:     id.Short(),
		TargetID:       targetID,
		ScenarioID:     scenarioID,
		Status:         domain.RunQueued,
		CleanupStatus:  domain.CleanupNotRequired,
		HarnessVersion: harnessVersion,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.CreateRun(ctx, run); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

// GetRun returns one durable run summary.
func (s *Service) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return s.store.GetRun(ctx, id)
}

// ListRuns returns filtered durable history.
func (s *Service) ListRuns(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	return s.store.ListRuns(ctx, filter)
}

// ListRunEvents returns the durable timeline for a run.
func (s *Service) ListRunEvents(ctx context.Context, id string) ([]domain.RunEvent, error) {
	return s.store.ListRunEvents(ctx, id)
}

// RecoverInterruptedRuns performs idempotent startup recovery.
func (s *Service) RecoverInterruptedRuns(ctx context.Context) (int, error) {
	return s.store.RecoverInterruptedRuns(ctx, s.now().UTC())
}

// CreateTarget validates and persists target metadata without resolving credentials.
func (s *Service) CreateTarget(ctx context.Context, input domain.TargetInput) (domain.Target, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if input.APIProfile == "" {
		input.APIProfile = "public-v1"
	}
	if err := input.Validate(); err != nil {
		return domain.Target{}, err
	}
	id, err := domain.NewTargetID(s.random)
	if err != nil {
		return domain.Target{}, fmt.Errorf("create target id: %w", err)
	}
	now := s.now().UTC()
	target := domain.Target{
		ID:          id,
		DisplayName: input.DisplayName,
		BaseURL:     input.BaseURL,
		APIProfile:  input.APIProfile,
		TLS:         input.TLS,
		Credential:  input.Credential,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateTarget(ctx, target); err != nil {
		return domain.Target{}, err
	}
	return target, nil
}

// ListTargets returns sanitized target metadata.
func (s *Service) ListTargets(ctx context.Context) ([]domain.Target, error) {
	return s.store.ListTargets(ctx)
}

// GetTarget returns sanitized target metadata.
func (s *Service) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	return s.store.GetTarget(ctx, id)
}
