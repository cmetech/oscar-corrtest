package oscar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const compactEvidencePath = "/api/v1/alerts/correlation/compact/evidence"

// Scope must come from the authenticated installed-rule runtime readback.
// It is an inventory identity, not a proof that every source alert was admitted.
type CompactEvidenceScope struct {
	RuleID   int64  `json:"rule_id"`
	Epoch    string `json:"epoch"`
	Revision string `json:"revision"`
}
type CompactEvidenceObservation struct {
	StartedAt    string          `json:"started_at"`
	CompletedAt  string          `json:"completed_at"`
	HTTPStatus   int             `json:"http_status"`
	Body         json.RawMessage `json:"body"`
	FailureClass string          `json:"failure_class"`
}
type CompactEvidenceReader struct {
	client *Client
	route  installedBase
}

func NewCompactEvidenceReader(target domain.Target, options Options) (*CompactEvidenceReader, error) {
	if strings.TrimSpace(target.ID) == "" || target.Credential.Kind == "" || target.TLS.Insecure || target.OrdinaryWebhook != nil || options.HTTPClient != nil || options.InputPath != "" {
		return nil, fmt.Errorf("compact evidence requires authenticated standard target with verified TLS")
	}
	if err := (domain.TargetInput{DisplayName: "compact evidence", BaseURL: target.BaseURL, TLS: target.TLS, Credential: target.Credential}).Validate(); err != nil {
		return nil, fmt.Errorf("compact evidence target invalid")
	}
	route, err := installedBaseURL(target.BaseURL, "/ext/mw")
	if err != nil || route.scheme != "https" {
		return nil, fmt.Errorf("compact evidence requires exact HTTPS root or /ext/mw origin")
	}
	client, err := New(target, options)
	if err != nil || client.credential == "" {
		return nil, fmt.Errorf("compact evidence client unavailable")
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("compact evidence transport unavailable")
	}
	transport.DisableCompression = true
	// Avoid idle-connection replay of this one-shot observation.
	transport.DisableKeepAlives = true
	client.http.Transport = compactEvidenceTransport{base: transport, route: route}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.http.Timeout = 10 * time.Second
	return &CompactEvidenceReader{client: client, route: route}, nil
}

type compactEvidenceTransport struct {
	base  http.RoundTripper
	route installedBase
}

func (t compactEvidenceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	path, err := t.route.path(r)
	if err != nil || path != compactEvidencePath || r.Method != http.MethodGet || r.Body != nil || r.URL.ForceQuery {
		return nil, fmt.Errorf("compact evidence route refused")
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 3 {
		return nil, fmt.Errorf("compact evidence query refused")
	}
	for _, key := range []string{"rule_id", "epoch", "revision"} {
		if len(q[key]) != 1 {
			return nil, fmt.Errorf("compact evidence query refused")
		}
	}
	id, err := strconv.ParseInt(q.Get("rule_id"), 10, 64)
	if err != nil || strconv.FormatInt(id, 10) != q.Get("rule_id") || !validCompactScope(CompactEvidenceScope{id, q.Get("epoch"), q.Get("revision")}) {
		return nil, fmt.Errorf("compact evidence scope refused")
	}
	return t.base.RoundTrip(r)
}

// Read makes exactly one GET. HTTP failures never preserve server error bodies;
// bounded malformed HTTP-200 bytes are retained for private forensic replay.
func (r *CompactEvidenceReader) Read(ctx context.Context, scope CompactEvidenceScope) (obs CompactEvidenceObservation, err error) {
	obs.StartedAt = r.client.now().UTC().Format(time.RFC3339Nano)
	defer func() { obs.CompletedAt = r.client.now().UTC().Format(time.RFC3339Nano) }()
	fail := func(class string) (CompactEvidenceObservation, error) {
		obs.FailureClass = class
		return obs, fmt.Errorf("compact evidence %s", class)
	}
	if !validCompactScope(scope) {
		return fail("invalid_scope")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u := *r.client.baseURL
	u.Path = r.route.prefix + compactEvidencePath
	u.RawQuery = url.Values{"rule_id": {strconv.FormatInt(scope.RuleID, 10)}, "epoch": {scope.Epoch}, "revision": {scope.Revision}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fail("request_unavailable")
	}
	req.Header.Set("X-API-Key", r.client.credential)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	response, err := r.client.http.Do(req)
	if err != nil {
		return fail("transport_unavailable")
	}
	defer response.Body.Close()
	obs.HTTPStatus = response.StatusCode
	if response.StatusCode != 200 {
		return fail("http_" + strconv.Itoa(response.StatusCode))
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fail("unsupported_encoding")
	}
	if response.ContentLength > maxResponseBytes {
		return fail("response_too_large")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fail("response_unavailable")
	}
	if len(raw) > maxResponseBytes {
		return fail("response_too_large")
	}
	obs.Body = raw
	if _, err = ValidateCompactEvidence(raw, scope); err != nil {
		return fail("invalid_evidence")
	}
	return obs, nil
}

// ValidateCompactEvidenceObservation is pure and refuses all incomplete/failure
// exchanges. It does not assert clock synchronization or source-coverage bounds.
func ValidateCompactEvidenceObservation(scope CompactEvidenceScope, obs CompactEvidenceObservation) error {
	start, e1 := time.Parse(time.RFC3339Nano, obs.StartedAt)
	end, e2 := time.Parse(time.RFC3339Nano, obs.CompletedAt)
	if e1 != nil || e2 != nil || end.Before(start) || obs.HTTPStatus != 200 || obs.FailureClass != "" {
		return fmt.Errorf("compact evidence exchange incomplete")
	}
	_, err := ValidateCompactEvidence(obs.Body, scope)
	return err
}
