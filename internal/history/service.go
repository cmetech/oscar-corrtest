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

// Service coordinates validation, identifiers, and timestamps around repositories.
type Service struct {
	store  targetStore
	now    func() time.Time
	random io.Reader
}

// New constructs a durable history service.
func New(store targetStore, now func() time.Time, random io.Reader) *Service {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	return &Service{store: store, now: now, random: random}
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
