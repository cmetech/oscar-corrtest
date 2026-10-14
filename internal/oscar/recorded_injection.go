package oscar

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
)

// InjectionRequestEvidence describes the exact prepared HTTP body and initial
// destination, not transport completion, source ordering or receiver acceptance.
// Values contain no credential/header data and share no mutable body bytes.
type InjectionRequestEvidence struct {
	Format      string `json:"format"`
	TargetID    string `json:"targetId"`
	APIProfile  string `json:"apiProfile"`
	Method      string `json:"method"`
	Endpoint    string `json:"endpoint"`
	ContentType string `json:"contentType"`
	BodyBytes   int    `json:"bodyBytes"`
	BodySHA256  string `json:"bodySha256"`
	BodyBase64  string `json:"bodyBase64"`
}

type InjectionRecorder func(InjectionRequestEvidence) error

const maxRecordedInjectionBytes = 1 << 20

// PrepareInjection freezes only the direct Alert Engine body and safe initial
// destination. Callers must commit evidence before sending the returned bytes.
func PrepareInjection(alert compiler.AlertPlan, targetID, endpoint string) (InjectionRequestEvidence, []byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return InjectionRequestEvidence{}, nil, fmt.Errorf("recorded injection requires a credential-free HTTP(S) endpoint without query or fragment")
	}
	payload, err := BuildAlertRequest(alert)
	if err != nil {
		return InjectionRequestEvidence{}, nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return InjectionRequestEvidence{}, nil, fmt.Errorf("encode recorded injection: %w", err)
	}
	if len(body) > maxRecordedInjectionBytes {
		return InjectionRequestEvidence{}, nil, fmt.Errorf("recorded injection body exceeds 1 MiB")
	}
	sum := sha256.Sum256(body)
	evidence := InjectionRequestEvidence{Format: "corrtest-alert-engine-request-v1", TargetID: targetID, APIProfile: "public-v1", Method: http.MethodPost, Endpoint: endpoint, ContentType: "application/json", BodyBytes: len(body), BodySHA256: hex.EncodeToString(sum[:]), BodyBase64: base64.StdEncoding.EncodeToString(body)}
	return evidence, body, nil
}

// InjectRecorded commits the supplied recorder before any HTTP attempt, then
// sends exactly the frozen body. Failure never authorizes an automatic retry.
func (c *Client) InjectRecorded(ctx context.Context, alert compiler.AlertPlan, beforeSend InjectionRecorder) (InjectionResult, error) {
	if beforeSend == nil {
		return InjectionResult{}, fmt.Errorf("recorded injection requires a before-send recorder")
	}
	// Check the original URL before deriving a path; otherwise query credentials
	// could be silently removed from the recorded binding.
	if c.baseURL.User != nil || c.baseURL.RawQuery != "" || c.baseURL.ForceQuery || c.baseURL.Fragment != "" {
		return InjectionResult{}, fmt.Errorf("recorded injection target contains forbidden userinfo, query or fragment")
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + alertsPath
	evidence, body, err := PrepareInjection(alert, c.targetID, endpoint.String())
	if err != nil {
		return InjectionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return InjectionResult{}, err
	}
	if err := beforeSend(evidence); err != nil {
		return InjectionResult{}, fmt.Errorf("record injection before send: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return InjectionResult{}, err
	}
	// Copy only this request's policy; shared client and other methods retain
	// their configured redirect behavior. A redirect remains an HTTP error.
	transport := *c.http
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	status, raw, err := c.doBytes(ctx, http.MethodPost, alertsPath, &endpoint, body, &transport)
	if err != nil {
		return InjectionResult{}, err
	}
	return classifyInjection(status, raw), nil
}
