package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

type InstalledPackInputEvidence struct {
	ManifestSHA256 string                   `json:"manifestSha256"`
	RunID          string                   `json:"runId"`
	Index          int                      `json:"index"`
	Request        InjectionRequestEvidence `json:"request"`
	Expected       ExistingFeedIdentity     `json:"expected"`
}

type InstalledPackInputResult struct {
	InjectionResult
	ProfileRevisions []string `json:"profileRevisions"`
}

// InstalledPackInput only sends the kit's fifteen initial firings. The caller
// must durably retain UNKNOWN before send and each outcome before advancing;
// reopening requires inspecting the original journal, never a new sender.
type InstalledPackInput struct {
	mu       sync.Mutex
	manifest InstalledPackManifest
	scenario testpack.Scenario
	startsAt time.Time
	client   *Client
	next     int
	stopped  bool
}

func NewInstalledPackInput(kit *testpack.Kit, runID string, startsAt time.Time, target domain.Target, options Options) (*InstalledPackInput, error) {
	if kit == nil || !installedInputContractMatches(kit.Bindings(), kit.Scenario()) {
		return nil, fmt.Errorf("installed input requires a verified fixed-contract kit")
	}
	manifest, err := FreezeInstalledPackInput(InstalledPackInputSpec{APIVersion: installedInputVersion, ManifestSHA256: kit.Bindings().ManifestSHA256, QualificationRun: runID, StartsAt: startsAt, ReceiverTargetID: target.ID, ReceiverBaseURL: target.BaseURL})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("installed input requires explicit receiver credential and standard verified transport")
	}
	if err := (domain.TargetInput{DisplayName: "installed Flood receiver", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	if _, err := installedBaseURL(target.BaseURL, "/webhookreceiver"); err != nil {
		return nil, err
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("installed receiver credential is empty")
	}
	client.profilePrecondition = `"` + kit.Bindings().ProfileEffectiveSHA256 + `"`
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &InstalledPackInput{manifest: manifest, scenario: kit.Scenario(), startsAt: startsAt, client: client}, nil
}

// Prepare delegates to the same pure fixed manifest used by durable readers.
func (input *InstalledPackInput) Prepare(index int) (InstalledPackInputEvidence, []byte, error) {
	if input.client == nil {
		return InstalledPackInputEvidence{}, nil, fmt.Errorf("installed input is not configured")
	}
	return input.manifest.Prepare(index)
}

// SendRecorded never retries or continues after an uncertain result, including
// a recorder error whose commit status this transport cannot know.
func (input *InstalledPackInput) SendRecorded(ctx context.Context, index int, record func(InstalledPackInputEvidence) error) (InstalledPackInputResult, error) {
	result := InstalledPackInputResult{InjectionResult: InjectionResult{Class: InjectionIndeterminate}}
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.client == nil || record == nil || input.stopped || index != input.next {
		return result, fmt.Errorf("installed input requires next unattempted input and recorder")
	}
	evidence, body, err := input.Prepare(index)
	if err != nil {
		return result, err
	}
	input.stopped = true
	if err := ctx.Err(); err != nil {
		return result, err
	}
	deadline := input.startsAt.Add(time.Duration(input.scenario.Budgets.WindowSeconds) * time.Second)
	now := input.client.now()
	remaining := deadline.Sub(now)
	if remaining <= 0 || now.Before(input.startsAt) {
		return result, fmt.Errorf("installed firing window expired or has not started")
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	if err := record(evidence); err != nil {
		return result, fmt.Errorf("record installed input before send: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	now = input.client.now()
	if now.Before(input.startsAt) || !now.Before(deadline) {
		return result, fmt.Errorf("installed firing window changed while recording")
	}
	endpoint, _ := url.Parse(evidence.Request.Endpoint)
	status, raw, headers, err := input.client.doPreparedBytesWithHeaders(ctx, http.MethodPost, endpoint.Path, endpoint, body, input.client.http, &preparedRequestHeaders{host: endpoint.Host, userAgent: "oscar-corrtest/installed-pack-v1"})
	result.StatusCode, result.Body = status, raw
	result.ProfileRevisions = append([]string(nil), headers.Values("X-Oscar-Profile-Revision")...)
	if err != nil {
		return result, err
	}
	now = input.client.now()
	if ctx.Err() != nil || now.Before(input.startsAt) || !now.Before(deadline) {
		return result, fmt.Errorf("installed acknowledgment arrived outside firing window")
	}
	fields, err := installedAuditObject(raw, "status")
	var acknowledgment string
	if err != nil || len(fields) != 1 || json.Unmarshal(fields["status"], &acknowledgment) != nil || acknowledgment != "ok" || status != http.StatusOK {
		return result, fmt.Errorf("installed receiver acknowledgment is absent or ambiguous")
	}
	revisions := result.ProfileRevisions
	if len(revisions) != 1 || revisions[0] != input.manifest.revision {
		return result, fmt.Errorf("installed receiver profile acknowledgment differs")
	}
	result.Class = InjectionForwarded
	input.next++
	input.stopped = false
	return result, nil
}
