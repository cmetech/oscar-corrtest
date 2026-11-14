// Package workerclient is the controller's HTTPS client for the campaign worker
// routes (internal/web/campaign_api.go) served on the beast01 worker.
package workerclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
)

const base = "/api/v1/campaign-worker"

// Client talks to one worker. The bearer token is re-read from tokenFile on every
// request (so rotation needs no restart) and is never logged or put in errors.
type Client struct {
	baseURL   string
	tokenFile string
	http      *http.Client
	// per-call deadlines for the non-streaming calls (shortened in tests)
	short, medium, long time.Duration
	maxStream           int
}

// APIError is returned for every non-2xx JSON error from the worker.
type APIError struct {
	Status        int
	Code, Message string
	sentinel      error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("worker returned %d %s: %s", e.Status, e.Code, e.Message)
}

// Unwrap exposes workerapi.ErrLaneNotFound / ErrLaneExists for errors.Is.
func (e *APIError) Unwrap() error { return e.sentinel }

const (
	maxLine   = 64 << 10
	maxStream = 512 << 20
)

// Ledger cap errors, matchable with errors.Is.
var (
	ErrLedgerLineTooLong = errors.New("worker ledger line exceeds 64 KiB")
	ErrLedgerTooLarge    = errors.New("worker ledger stream exceeds 512 MiB")
)

// New builds a client. caFile is added to the system roots; empty means system roots only.
func New(baseURL, tokenFile, caFile string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("worker base URL %q must be an https URL", baseURL)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) // #nosec G304 -- operator-configured CA path from the campaign/worker config, not request input.
		if err != nil {
			return nil, fmt.Errorf("read worker CA file: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in worker CA file %s", caFile)
		}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), tokenFile: tokenFile, maxStream: maxStream, short: 10 * time.Second, medium: 30 * time.Second, long: 120 * time.Second, http: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}},
	}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+base+path, rd)
	if err != nil {
		return nil, err
	}
	tok, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return nil, errors.New("read worker token file failed") // no path/err detail: keep it quiet
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL wrapper
		}
		return nil, fmt.Errorf("worker request %s %s: %w", method, path, err)
	}
	return resp, nil
}

// call sends a request and decodes the {"data":…} envelope into out (may be nil).
func (c *Client) call(ctx context.Context, d time.Duration, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	resp, err := c.do(ctx, method, path, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return apiErr(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil {
		return fmt.Errorf("worker %s %s: malformed response", method, path)
	}
	return json.Unmarshal(env.Data, out)
}

func apiErr(status int, raw []byte) error {
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	ae := &APIError{Status: status, Code: e.Error.Code, Message: e.Error.Message}
	if ae.Code == "" { // non-JSON body, e.g. the auth middleware's plain-text 401
		ae.Code = "http_" + http.StatusText(status)
		if status == http.StatusUnauthorized {
			ae.Code = "unauthorized"
		}
	}
	switch ae.Code {
	case "lane_not_found":
		ae.sentinel = workerapi.ErrLaneNotFound
	case "lane_exists":
		ae.sentinel = workerapi.ErrLaneExists
	}
	return ae
}

func (c *Client) Info(ctx context.Context) (workerapi.Info, error) {
	var d struct {
		Version string `json:"version"`
		Now     string `json:"now"`
		Disk    uint64 `json:"diskFreeBytes"`
	}
	if err := c.call(ctx, c.short, http.MethodGet, "/info", nil, &d); err != nil {
		return workerapi.Info{}, err
	}
	now, err := time.Parse(time.RFC3339Nano, d.Now)
	if err != nil {
		return workerapi.Info{}, fmt.Errorf("worker info: bad clock %q", d.Now)
	}
	return workerapi.Info{Version: d.Version, Now: now, DiskFreeBytes: d.Disk}, nil
}

func (c *Client) Start(ctx context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error) {
	var st workerapi.LaneStatus
	err := c.call(ctx, c.medium, http.MethodPost, "/lanes", s, &st)
	return st, err
}

// Heartbeat keeps the lane alive and returns its state and last error.
func (c *Client) Heartbeat(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	var st workerapi.LaneStatus
	err := c.call(ctx, c.short, http.MethodPost, "/lanes/"+url.PathEscape(id)+"/heartbeat", nil, &st)
	return st, err
}

func (c *Client) Status(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	var st workerapi.LaneStatus
	err := c.call(ctx, c.short, http.MethodGet, "/lanes/"+url.PathEscape(id), nil, &st)
	return st, err
}

func (c *Client) Stop(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	var st workerapi.LaneStatus
	err := c.call(ctx, c.medium, http.MethodPost, "/lanes/"+url.PathEscape(id)+"/stop", nil, &st)
	return st, err
}

func (c *Client) ResolveAll(ctx context.Context, id string) (int, error) {
	var d struct {
		Resolved int `json:"resolved"`
	}
	err := c.call(ctx, c.long, http.MethodPost, "/lanes/"+url.PathEscape(id)+"/resolve-all", nil, &d)
	return d.Resolved, err
}

// Ledger streams the lane's NDJSON rows to w, excluding the terminal end record.
// It fails if the stream has no end record (truncated), the end says ok:false, a
// line exceeds 64 KiB, or the stream exceeds 512 MiB. Rows written before an
// error remain in w, so callers must write to a temp file and rename only on nil.
func (c *Client) Ledger(ctx context.Context, id string, w io.Writer) error {
	resp, err := c.do(ctx, http.MethodGet, "/lanes/"+url.PathEscape(id)+"/ledger", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return apiErr(resp.StatusCode, raw)
	}
	r := bufio.NewReaderSize(io.LimitReader(resp.Body, int64(c.maxStream)+1), maxLine)
	total := 0
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return ErrLedgerLineTooLong
		}
		total += len(line)
		if total > c.maxStream {
			return ErrLedgerTooLarge
		}
		if len(line) > 0 && line[len(line)-1] == '\n' { // a partial last line is never accepted
			var end struct {
				Type  string `json:"type"`
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if json.Unmarshal(line, &end) == nil && end.Type == "end" {
				if !end.OK {
					return fmt.Errorf("worker ledger stream failed: %s", end.Error)
				}
				return nil
			}
			if _, werr := w.Write(line); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("worker ledger stream truncated: no end record")
			}
			return fmt.Errorf("worker ledger stream: %w", err)
		}
	}
}

// SetTimeouts overrides the per-call deadlines (short, medium, long); for tests.
func (c *Client) SetTimeouts(short, medium, long time.Duration) {
	c.short, c.medium, c.long = short, medium, long
}
