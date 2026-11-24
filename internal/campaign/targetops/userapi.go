package targetops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UserAPI calls OSCAR's public API (/ext/mw/api/v1) with a user account's API
// key. Report artifacts and on-demand task runs refuse keys owned by a system
// account, so the v4 lanes use the first front-office user, which is a user.
type UserAPI struct {
	Root string // scheme://host of the target
	Key  string
	HTTP *http.Client
}

// StatusError is a definite non-2xx answer.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

func (u UserAPI) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	target := u.Root + "/ext/mw/api/v1" + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", u.Key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &StatusError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw[:min(len(raw), 512)]))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// RunTask starts the task named (or with UUID) job: POST /tasks/run/{job}. It
// returns the Celery run id.
func (u UserAPI) RunTask(ctx context.Context, job string) (string, error) {
	var out struct {
		ID string `json:"celery_task_id"`
	}
	if err := u.do(ctx, http.MethodPost, "/tasks/run/"+url.PathEscape(job), nil, map[string]any{}, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("task run for %s returned no celery_task_id", job)
	}
	return out.ID, nil
}

// TaskResult long-polls GET /tasks/result/{id}?timeout=5. done is false while
// the run is pending (including the 404 before its History row is flushed).
func (u UserAPI) TaskResult(ctx context.Context, id string) (status string, done bool, err error) {
	var out struct {
		Status string `json:"status"`
	}
	err = u.do(ctx, http.MethodGet, "/tasks/result/"+url.PathEscape(id), url.Values{"timeout": {"5"}}, nil, &out)
	if se, ok := err.(*StatusError); ok && se.Status == http.StatusNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch s := strings.ToUpper(out.Status); s {
	case "SUCCESS", "FAILURE", "REVOKED":
		return s, true, nil
	default:
		return s, false, nil
	}
}

// ReportInfo is one report in GET /reports.
type ReportInfo struct {
	ID         string `json:"id"`
	Key        string `json:"report_key"`
	SetupState string `json:"setup_state"`
}

// Report finds the report with key; an artifact report must also be approved
// (setup_state "approved") before a manual run is admitted.
func (u UserAPI) Report(ctx context.Context, key string) (ReportInfo, error) {
	var list []ReportInfo
	if err := u.do(ctx, http.MethodGet, "/reports", url.Values{"limit": {"100"}}, nil, &list); err != nil {
		return ReportInfo{}, err
	}
	for _, r := range list {
		if r.Key == key {
			return r, nil
		}
	}
	return ReportInfo{}, fmt.Errorf("report %s not found", key)
}

// RunReport requests a manual artifact run of report id over [start, end).
// OSCAR makes requestID (a UUID) the run id, so a retry is idempotent; the
// run id it answers is returned (requestID when it answers none).
func (u UserAPI) RunReport(ctx context.Context, id, requestID string, start, end time.Time) (runID, state string, err error) {
	const iso = "2006-01-02T15:04:05+00:00"
	var out struct {
		RunID string `json:"run_id"`
		State string `json:"state"`
	}
	err = u.do(ctx, http.MethodPost, "/reports/"+url.PathEscape(id)+"/run", nil,
		map[string]string{"request_id": requestID, "start": start.UTC().Format(iso), "end": end.UTC().Format(iso)}, &out)
	if out.RunID == "" {
		out.RunID = requestID
	}
	return out.RunID, out.State, err
}

// ReportRunState is GET /reports/runs/{id}: its state and error code.
func (u UserAPI) ReportRunState(ctx context.Context, runID string) (state, errorCode string, err error) {
	var out struct {
		State     string  `json:"state"`
		ErrorCode *string `json:"error_code"`
	}
	if err = u.do(ctx, http.MethodGet, "/reports/runs/"+url.PathEscape(runID), nil, nil, &out); err != nil {
		return "", "", err
	}
	if out.ErrorCode != nil {
		errorCode = *out.ErrorCode
	}
	return out.State, errorCode, nil
}

// ReportManifest is GET /reports/runs/{id}/artifacts (first page): the
// manifest object and the committed parts.
type ReportManifest struct {
	Manifest struct {
		ID        string `json:"id"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"size_bytes"`
	} `json:"manifest"`
	Items []struct {
		ID        string `json:"id"`
		SizeBytes int64  `json:"size_bytes"`
	} `json:"items"`
}

func (u UserAPI) ReportArtifacts(ctx context.Context, runID string) (ReportManifest, error) {
	var m ReportManifest
	err := u.do(ctx, http.MethodGet, "/reports/runs/"+url.PathEscape(runID)+"/artifacts", url.Values{"after": {"0"}, "limit": {"50"}}, nil, &m)
	return m, err
}

// VerifyManifest downloads the manifest object from object storage through its
// presigned URL and checks its size and sha256 against the listing.
func (u UserAPI) VerifyManifest(ctx context.Context, runID string, m ReportManifest) error {
	if m.Manifest.ID == "" || m.Manifest.SHA256 == "" || len(m.Items) == 0 {
		return fmt.Errorf("artifact listing has no manifest or no committed part")
	}
	var link struct {
		URL string `json:"url"`
	}
	if err := u.do(ctx, http.MethodGet, "/reports/runs/"+url.PathEscape(runID)+"/artifacts/"+url.PathEscape(m.Manifest.ID)+"/download", nil, nil, &link); err != nil {
		return fmt.Errorf("download link: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		return err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("object storage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("object storage: HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("object storage: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); n != m.Manifest.SizeBytes || !strings.EqualFold(got, m.Manifest.SHA256) {
		return fmt.Errorf("manifest read back %d bytes sha256 %s, listing says %d bytes %s", n, got, m.Manifest.SizeBytes, m.Manifest.SHA256)
	}
	return nil
}
