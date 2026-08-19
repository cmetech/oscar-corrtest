package domain

import (
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CredentialKind identifies where a credential will be resolved at request time.
type CredentialKind string

const (
	CredentialEnvironment CredentialKind = "env"
	CredentialFile        CredentialKind = "file"
	CredentialSystemd     CredentialKind = "systemd"
)

var referenceNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CredentialRef names a credential source without containing its value.
type CredentialRef struct {
	Kind      CredentialKind `json:"kind,omitempty"`
	Reference string         `json:"reference,omitempty"`
}

// TLSPolicy contains non-secret transport verification settings.
type TLSPolicy struct {
	Insecure bool   `json:"insecure"`
	CAPath   string `json:"caPath,omitempty"`
}

// TargetInput is sanitized metadata accepted when creating a target.
type TargetInput struct {
	DisplayName string        `json:"displayName"`
	BaseURL     string        `json:"baseUrl"`
	APIProfile  string        `json:"apiProfile,omitempty"`
	TLS         TLSPolicy     `json:"tls"`
	Credential  CredentialRef `json:"credential,omitempty"`
}

// Validate rejects target metadata that could embed credentials or weaken TLS ambiguously.
func (input TargetInput) Validate() error {
	if strings.TrimSpace(input.DisplayName) == "" {
		return fmt.Errorf("target display name is required")
	}
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("target base URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("target base URL cannot contain user info, query, or fragment")
	}
	if input.TLS.Insecure && input.TLS.CAPath != "" {
		return fmt.Errorf("insecure TLS and a custom CA cannot be combined")
	}
	if input.TLS.CAPath != "" && !filepath.IsAbs(input.TLS.CAPath) {
		return fmt.Errorf("custom CA path must be absolute")
	}
	switch input.Credential.Kind {
	case "":
		if input.Credential.Reference != "" {
			return fmt.Errorf("credential reference requires a kind")
		}
	case CredentialEnvironment, CredentialSystemd:
		if !referenceNamePattern.MatchString(input.Credential.Reference) {
			return fmt.Errorf("credential reference name is invalid")
		}
	case CredentialFile:
		if !filepath.IsAbs(input.Credential.Reference) {
			return fmt.Errorf("credential file path must be absolute")
		}
	default:
		return fmt.Errorf("unsupported credential reference kind %q", input.Credential.Kind)
	}
	return nil
}

// Target is the durable, sanitized OSCAR endpoint metadata.
type Target struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"displayName"`
	BaseURL     string        `json:"baseUrl"`
	APIProfile  string        `json:"apiProfile"`
	TLS         TLSPolicy     `json:"tls"`
	Credential  CredentialRef `json:"credential,omitempty"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
}

// NewTargetID returns an opaque target identifier using the run-ID entropy contract.
func NewTargetID(random io.Reader) (string, error) {
	runID, err := NewRunID(random)
	if err != nil {
		return "", err
	}
	return "tgt_" + runID.String()[4:], nil
}
