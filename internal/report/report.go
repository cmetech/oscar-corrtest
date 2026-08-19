// Package report builds the versioned canonical JSON report from durable facts.
package report

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const APIVersion = "corrtest.oscar/v1alpha1"

// ArtifactEvidence combines immutable manifest metadata with current integrity.
type ArtifactEvidence struct {
	Manifest  artifact.Manifest
	Integrity artifact.Integrity
}

// Input contains stored facts used to build a report.
type Input struct {
	HarnessVersion string
	Target         domain.Target
	Run            domain.Run
	Events         []domain.RunEvent
	Artifacts      []ArtifactEvidence
	Warnings       []string
}

// Document is the stable report contract.
type Document struct {
	APIVersion     string            `json:"apiVersion"`
	HarnessVersion string            `json:"harnessVersion"`
	Target         TargetSummary     `json:"target"`
	Run            RunSummary        `json:"run"`
	Events         []domain.RunEvent `json:"events"`
	Artifacts      []ArtifactSummary `json:"artifacts"`
	Warnings       []string          `json:"warnings,omitempty"`
}

// TargetSummary intentionally excludes credential references.
type TargetSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	BaseURL     string `json:"baseUrl"`
	APIProfile  string `json:"apiProfile"`
	TLSInsecure bool   `json:"tlsInsecure"`
}

// RunSummary keeps lifecycle, verdict, and cleanup as distinct fields.
type RunSummary struct {
	ID            string               `json:"id"`
	ShortToken    string               `json:"shortToken"`
	Status        domain.RunStatus     `json:"status"`
	Verdict       domain.Verdict       `json:"verdict,omitempty"`
	CleanupStatus domain.CleanupStatus `json:"cleanupStatus"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	TerminalError string               `json:"terminalError,omitempty"`
}

// ArtifactSummary is portable integrity metadata.
type ArtifactSummary struct {
	Path      string             `json:"path"`
	MIMEType  string             `json:"mimeType"`
	SHA256    string             `json:"sha256"`
	ByteSize  int64              `json:"byteSize"`
	Integrity artifact.Integrity `json:"integrity"`
}

// Build normalizes ordering and strips credential-bearing target fields.
func Build(input Input) Document {
	events := append([]domain.RunEvent(nil), input.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	evidence := append([]ArtifactEvidence(nil), input.Artifacts...)
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].Manifest.RelativePath < evidence[j].Manifest.RelativePath })
	artifacts := make([]ArtifactSummary, 0, len(evidence))
	warnings := append([]string(nil), input.Warnings...)
	for _, item := range evidence {
		artifacts = append(artifacts, ArtifactSummary{
			Path: item.Manifest.RelativePath, MIMEType: item.Manifest.MIMEType, SHA256: item.Manifest.SHA256,
			ByteSize: item.Manifest.ByteSize, Integrity: item.Integrity,
		})
		if item.Integrity != artifact.IntegrityValid {
			warnings = append(warnings, "artifact "+item.Manifest.RelativePath+": "+string(item.Integrity))
		}
	}
	sort.Strings(warnings)
	return Document{
		APIVersion: APIVersion, HarnessVersion: input.HarnessVersion,
		Target: TargetSummary{
			ID: input.Target.ID, DisplayName: input.Target.DisplayName, BaseURL: input.Target.BaseURL,
			APIProfile: input.Target.APIProfile, TLSInsecure: input.Target.TLS.Insecure,
		},
		Run: RunSummary{
			ID: input.Run.ID, ShortToken: input.Run.ShortToken, Status: input.Run.Status, Verdict: input.Run.Verdict,
			CleanupStatus: input.Run.CleanupStatus, CreatedAt: input.Run.CreatedAt, UpdatedAt: input.Run.UpdatedAt,
			TerminalError: input.Run.TerminalError,
		},
		Events: events, Artifacts: artifacts, Warnings: warnings,
	}
}

// Marshal returns compact deterministic JSON for a normalized document.
func Marshal(document Document) ([]byte, error) { return json.Marshal(document) }
