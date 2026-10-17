package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// ExistingFeedRecorder must durably retain the full evidence before returning nil.
// A reopened caller must reject previously recorded attempt identities itself.
type ExistingFeedRecorder func(ExistingFeedRequestEvidence) error

// ExistingFeedLabInput requires an independently owned lab with the pinned registry
// and read-only Mapping snapshots installed; manifest values alone prove none of these.
type ExistingFeedLabInput struct {
	mu        sync.Mutex
	manifest  ExistingFeedManifest
	transport *Client
	attempted [6]bool
}

func NewExistingFeedLabInput(manifest ExistingFeedManifest, receiver domain.Target, options Options) (*ExistingFeedLabInput, error) {
	if _, err := FreezeExistingFeedManifest(manifest.spec); err != nil {
		return nil, err
	}
	if receiver.ID != manifest.spec.TargetID || receiver.BaseURL != manifest.spec.ReceiverOrigin || receiver.OrdinaryWebhook != nil || receiver.Credential.Kind == "" || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("existing-feed owned-lab transport requires matching target/origin, explicit receiver credential and standard isolated transport")
	}
	if err := (domain.TargetInput{DisplayName: "existing-feed owned lab receiver", BaseURL: receiver.BaseURL, TLS: receiver.TLS, Credential: receiver.Credential}).Validate(); err != nil {
		return nil, err
	}
	transport, err := New(receiver, options)
	if err != nil {
		return nil, err
	}
	if transport.credential == "" {
		return nil, fmt.Errorf("existing-feed receiver credential is empty")
	}
	transport.profilePrecondition = `"` + manifest.spec.ProfileHash + `"`
	transport.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ExistingFeedLabInput{manifest: manifest, transport: transport}, nil
}

// SendRecorded attempts a fixture once per instance after recording its complete evidence.
// Ambiguous results remain the caller's responsibility; this method never clears or retries.
func (input *ExistingFeedLabInput) SendRecorded(ctx context.Context, index int, record ExistingFeedRecorder) (InjectionResult, error) {
	result := InjectionResult{Class: InjectionIndeterminate}
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.transport == nil || record == nil {
		return result, fmt.Errorf("existing-feed requires owned-lab transport and recorder")
	}
	evidence, body, err := input.manifest.Prepare(index)
	if err != nil {
		return result, err
	}
	if len(body) > maxRecordedInjectionBytes {
		return result, fmt.Errorf("existing-feed request exceeds 1 MiB")
	}
	if input.attempted[index] {
		return result, fmt.Errorf("existing-feed fixture already recorded; reconciliation required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := record(evidence); err != nil {
		return result, fmt.Errorf("record existing-feed request before send: %w", err)
	}
	input.attempted[index] = true
	if err := ctx.Err(); err != nil {
		return result, err
	}
	endpoint, _ := url.Parse(evidence.Request.Endpoint)
	status, raw, headers, err := input.transport.doPreparedBytesWithHeaders(ctx, http.MethodPost, endpoint.Path, endpoint, body, input.transport.http, &preparedRequestHeaders{host: input.manifest.spec.RequestHost, userAgent: input.manifest.spec.UserAgent})
	result.StatusCode, result.Body = status, raw
	if err != nil {
		return result, err
	}
	var response struct {
		Status string `json:"status"`
	}
	if status == http.StatusOK && json.Unmarshal(raw, &response) == nil {
		revisions := headers.Values("X-Oscar-Profile-Revision")
		if response.Status == "ok" && len(revisions) == 1 && revisions[0] == input.manifest.spec.ProfileHash {
			result.Class = InjectionForwarded
		}
		if response.Status == "acl_denied" {
			result.Class = InjectionRejected
		}
	}
	return result, nil
}
