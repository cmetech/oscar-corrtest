package oscar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// ErrExistingFeedNativeNotObserved permits a bounded read-only observation poll.
// It never permits resending the recorded input.
var ErrExistingFeedNativeNotObserved = errors.New("existing-feed Native episode not observed")

// ExistingFeedNativeObserver reads only the finite externally owned Native lab.
// Registry/snapshot and endpoint ownership remain external trusted preconditions.
type ExistingFeedNativeObserver struct {
	manifest  ExistingFeedManifest
	transport *Client
}

func NewExistingFeedNativeObserver(manifest ExistingFeedManifest, target domain.Target, options Options) (*ExistingFeedNativeObserver, error) {
	if _, err := FreezeExistingFeedManifest(manifest.spec); err != nil {
		return nil, err
	}
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("existing-feed Native observer requires explicit owned target, credential and standard read-only transport")
	}
	if err := (domain.TargetInput{DisplayName: "owned existing-feed Native observer", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, err
	}
	origin, err := url.Parse(target.BaseURL)
	if err != nil || origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || strings.Contains(target.BaseURL, "#") {
		return nil, fmt.Errorf("existing-feed Native origin must not contain path, query or fragment")
	}
	client, err := New(target, options)
	if err != nil {
		return nil, err
	}
	if client.credential == "" {
		return nil, fmt.Errorf("existing-feed Native credential is empty")
	}
	client.http.Transport = existingFeedNativeAuth{base: client.http.Transport, password: client.credential}
	client.credential = ""
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ExistingFeedNativeObserver{manifest: manifest, transport: client}, nil
}

// Observe verifies exactly one full Native labels/start/fingerprint identity.
// A missing identity can be polled; changed or ambiguous observations fail closed.
func (observer *ExistingFeedNativeObserver) Observe(ctx context.Context, index int) (ExistingFeedNativeObservation, error) {
	var zero ExistingFeedNativeObservation
	if observer == nil || observer.transport == nil {
		return zero, fmt.Errorf("owned Native observer unavailable")
	}
	evidence, _, err := observer.manifest.Prepare(index)
	if err != nil {
		return zero, err
	}
	key := "node"
	if evidence.Manifest.Profile == "middleware_custom_alert" {
		key = "hostname"
	}
	query := url.Values{"filter": {key + "=" + strconv.Quote(evidence.Expected.Labels[key])}}
	status, raw, err := observer.transport.do(ctx, http.MethodGet, "/api/v2/alerts", query, nil)
	if err != nil {
		return zero, err
	}
	if status != http.StatusOK {
		return zero, fmt.Errorf("existing-feed Native response is not HTTP 200")
	}
	var rows []ExistingFeedNativeObservation
	if err = json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return zero, fmt.Errorf("invalid existing-feed Native response")
	}
	if len(rows) == 0 {
		return zero, ErrExistingFeedNativeNotObserved
	}
	if err = observer.manifest.VerifyNative(index, rows); err != nil {
		return zero, err
	}
	return rows[0], nil
}

// The private wrapper uses the qualified Native lab's fixed publisher account.
// It retains Client's TLS/timeouts/response limits and cannot forward a mutation.
type existingFeedNativeAuth struct {
	base     http.RoundTripper
	password string
}

func (auth existingFeedNativeAuth) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.Path != "/api/v2/alerts" {
		return nil, fmt.Errorf("owned Native observer permits only the fixed GET")
	}
	prepared := request.Clone(request.Context())
	prepared.Header.Del("X-API-Key")
	prepared.SetBasicAuth("publisher", auth.password)
	return auth.base.RoundTrip(prepared)
}
