package lane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SuppressionSubject is recorded before the first write and used across recovery.
type SuppressionSubject struct {
	AID, Source, TID, Owner, ControlID string
	Start, End                         time.Time
}

func (s SuppressionSubject) labels() map[string]string {
	return map[string]string{"aid": s.AID, "source_id": s.Source, "node": s.TID, "severity": "major"}
}

type silenceMatcher struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Regex bool   `json:"isRegex"`
	Equal bool   `json:"isEqual"`
}
type ownedSilence struct {
	ID        string                 `json:"id,omitempty"`
	CreatedBy string                 `json:"createdBy"`
	Comment   string                 `json:"comment"`
	StartsAt  time.Time              `json:"startsAt"`
	EndsAt    time.Time              `json:"endsAt"`
	Matchers  []silenceMatcher       `json:"matchers"`
	Status    struct{ State string } `json:"status,omitempty"`
}

func (c *amSender) nativeCall(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u, err := url.Parse(c.url)
	if err != nil || !strings.HasSuffix(u.Path, "/api/v2/alerts") {
		return errors.New("expected configured native v2 alerts route")
	}
	u.Path = strings.TrimSuffix(u.Path, "alerts") + path
	u.RawQuery = query.Encode()
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return errors.New("native request outcome unknown")
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("native suppression HTTP %d", res.StatusCode)
	}
	if out != nil {
		raw, err := io.ReadAll(io.LimitReader(res.Body, 262145))
		if err != nil || len(raw) > 262144 || json.Unmarshal(raw, out) != nil {
			return errors.New("incomplete native suppression readback")
		}
	}
	return nil
}
func (s SuppressionSubject) matches(r ownedSilence) bool {
	if r.CreatedBy != "CorrTest" || r.Comment != s.Owner || len(r.Matchers) != 4 {
		return false
	}
	want := s.labels()
	for _, m := range r.Matchers {
		v, ok := want[m.Name]
		if !ok || m.Regex || !m.Equal || v != m.Value {
			return false
		}
		delete(want, m.Name)
	}
	return len(want) == 0
}
func (c *amSender) Silence(ctx context.Context, s SuppressionSubject) (string, error) {
	if s.AID == "" || s.Source == "" || s.TID == "" || s.Owner == "" || !s.End.After(s.Start) {
		return "", errors.New("incomplete silence ownership")
	}
	labels := s.labels()
	var matchers []silenceMatcher
	for _, k := range []string{"aid", "source_id", "node", "severity"} {
		matchers = append(matchers, silenceMatcher{k, labels[k], false, true})
	}
	body := map[string]any{"createdBy": "CorrTest", "comment": s.Owner, "startsAt": s.Start, "endsAt": s.End, "matchers": matchers}
	var result struct {
		ID string `json:"silenceID"`
	}
	err := c.nativeCall(ctx, "POST", "silences", nil, body, &result)
	if err != nil {
		return "", err
	}
	if _, err = uuid.Parse(result.ID); err != nil {
		return "", errors.New("original silence creation outcome unknown")
	}
	return result.ID, nil
}
func (c *amSender) ExpireSilence(ctx context.Context, s SuppressionSubject) error {
	id := s.ControlID
	if id == "" {
		q := url.Values{"filter": {"aid=" + strconv.Quote(s.AID)}}
		var rows []ownedSilence
		if err := c.nativeCall(ctx, "GET", "silences", q, nil, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			if s.matches(r) {
				if id != "" {
					return errors.New("ambiguous original silence")
				}
				id = r.ID
			}
		}
	}
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("original silence identity unresolved")
	}
	path := "silence/" + id
	var r ownedSilence
	if err := c.nativeCall(ctx, "GET", path, nil, nil, &r); err != nil {
		return err
	}
	if r.ID != id || !s.matches(r) {
		return errors.New("foreign silence ownership")
	}
	if r.Status.State == "expired" {
		return nil
	}
	if err := c.nativeCall(ctx, "DELETE", path, nil, nil, nil); err != nil {
		return err
	}
	if err := c.nativeCall(ctx, "GET", path, nil, nil, &r); err != nil {
		return err
	}
	if r.ID != id || !s.matches(r) || r.Status.State != "expired" {
		return errors.New("original silence expiry unverified")
	}
	return nil
}
func (c *amSender) Inhibitor(ctx context.Context, s SuppressionSubject, clear bool) error {
	labels := s.labels()
	labels["severity"] = "info"
	labels["alertname"] = "CorrTestNativeInhibitor"
	labels["campaign_control"] = s.Owner
	labels["create_ticket"] = "false"
	end := s.End
	if clear {
		end = time.Now().UTC()
	}
	return c.nativeCall(ctx, "POST", "alerts", nil, []any{map[string]any{"labels": labels, "startsAt": s.Start, "endsAt": end}}, nil)
}
