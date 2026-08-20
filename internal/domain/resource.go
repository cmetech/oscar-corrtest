package domain

import (
	"fmt"
	"time"
)

type ResourceState string

const (
	ResourceProposed ResourceState = "PROPOSED"
	ResourceCreated  ResourceState = "CREATED"
	ResourceDeleted  ResourceState = "DELETED"
	ResourceUnknown  ResourceState = "UNKNOWN"
)

// Resource is the durable ownership proof for one external mutation.
type Resource struct {
	ID             string        `json:"id"`
	RunID          string        `json:"runId"`
	Kind           string        `json:"kind"`
	ExternalID     string        `json:"externalId,omitempty"`
	ExternalName   string        `json:"externalName"`
	OwnershipToken string        `json:"ownershipToken"`
	LifecycleState ResourceState `json:"lifecycleState"`
	CreatedAt      *time.Time    `json:"createdAt,omitempty"`
	DeletedAt      *time.Time    `json:"deletedAt,omitempty"`
	CleanupError   string        `json:"cleanupError,omitempty"`
}

func (resource Resource) Validate() error {
	if resource.ID == "" || resource.RunID == "" || resource.Kind == "" || resource.ExternalName == "" || resource.OwnershipToken == "" {
		return fmt.Errorf("resource ownership metadata is incomplete")
	}
	if resource.OwnershipToken != resource.RunID {
		return fmt.Errorf("resource ownership token must equal the full run id")
	}
	switch resource.LifecycleState {
	case ResourceProposed:
		if resource.ExternalID != "" || resource.CreatedAt != nil || resource.DeletedAt != nil {
			return fmt.Errorf("proposed resource cannot carry external creation evidence")
		}
	case ResourceCreated, ResourceDeleted, ResourceUnknown:
	default:
		return fmt.Errorf("resource lifecycle state %q is invalid", resource.LifecycleState)
	}
	return nil
}
