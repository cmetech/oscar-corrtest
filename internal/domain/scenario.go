package domain

import "time"

// ScenarioRecord is an immutable imported scenario source document.
type ScenarioRecord struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	APIVersion     string    `json:"apiVersion"`
	SourceDocument string    `json:"sourceDocument,omitempty"`
	SHA256         string    `json:"sha256"`
	BuiltIn        bool      `json:"builtIn"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
