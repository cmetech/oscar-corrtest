package targetops

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxBody = 16 << 20

// newHTTP builds a TLS-verifying client; caFile may be empty.
func newHTTP(caFile string, timeout time.Duration) (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) // #nosec G304 -- operator-selected CA path
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file contains no certificates")
		}
		cfg.RootCAs = pool
	}
	t.TLSClientConfig = cfg
	return &http.Client{Transport: t, Timeout: timeout}, nil
}

func readSecret(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- operator-selected credential path
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// doRaw performs a request and returns status and capped body. Errors never include request headers or bodies.
func doRaw(ctx context.Context, hc *http.Client, method, url string, body io.Reader, set func(*http.Request)) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	set(req)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s request failed", method)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return resp.StatusCode, nil, fmt.Errorf("%s response unreadable or too large", method)
	}
	return resp.StatusCode, raw, nil
}
