package lane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Sender delivers one event; a non-2xx reply is a status with a nil error.
type Sender interface {
	Send(ctx context.Context, e Event, runID string, lane string) (status int, err error)
}

type amSender struct {
	url    string
	client *http.Client
}

// NewAlertmanagerSender posts Alertmanager v2 alerts to ingestURL. TLS trusts
// the system pool plus caFile (when set); redirects and proxies are disabled.
func NewAlertmanagerSender(ingestURL string, caFile string, timeout time.Duration) (Sender, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in CA file %s", caFile)
		}
	}
	return &amSender{url: ingestURL, client: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		},
	}}, nil
}

func (s *amSender) Send(ctx context.Context, e Event, runID, lane string) (int, error) {
	ticket := "false"
	if e.Ticket {
		ticket = "snow"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	alert := map[string]any{
		"labels": map[string]string{
			"alertname": e.Identity, "campaign_run": runID, "campaign_lane": lane,
			"severity": e.Severity, "instance": e.Instance, "create_ticket": ticket,
		},
		"annotations": map[string]string{"summary": e.Identity, "description": "OSCAR alerting campaign"},
		"startsAt":    now,
	}
	if e.Kind == Resolve {
		alert["endsAt"] = now
	}
	body, err := json.Marshal([]any{alert})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, nil
}
