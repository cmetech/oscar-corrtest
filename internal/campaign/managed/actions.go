package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Decision struct {
	Allowed       bool
	Reason, Scope string
}
type Eligibility struct {
	HistoryID string `json:"history_id"`
	Episode   int    `json:"episode_number"`
	Actions   map[string]Decision
}
type CommandReceipt struct {
	Owner         int64                 `json:"-"`
	IssuedRequest string                `json:"-"`
	TicketID      string                `json:"ticket_id"`
	Error         struct{ Code string } `json:"error"`
	ItemStatus    int                   `json:"status_code"`
	StatusCode    int                   `json:"statusCode"`
	RequestID     string                `json:"request_id"`
	Operation     string                `json:"operation_id"`
	ResendID      string                `json:"resend_id"`
	AlertID       string                `json:"alert_id"`
	NativeState   string                `json:"native_state"`
	TargetCounts  stateCounts           `json:"native_target_counts"`
	Counts        stateCounts
	Code          string
	Outcome       string
	Record        struct {
		ID           string
		Episode      int `json:"episode_number"`
		Acknowledged bool
	}
	Results []CommandReceipt
}

// Counts are maps in the public response. Keep only protocol states in evidence.
type stateCounts map[string]int

func (s *stateCounts) UnmarshalJSON(data []byte) error {
	var values map[string]int
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	out := stateCounts{}
	for key, count := range values {
		if count < 0 {
			return errors.New("invalid native receipt count")
		}
		switch key {
		case "confirmed", "no_effect", "partial", "unknown", "pending", "held", "ready", "entered", "reserved", "accepted", "failed", "sent", "suppressed", "queued", "dispatching", "skipped":
		default:
			key = "unrecognized"
		}
		out[key] += count
	}
	*s = out
	return nil
}

type Actor struct {
	root, cookie string
	client       *http.Client
}

func NewActor(c Config, root string, hc *http.Client) (*Actor, error) {
	u, err := url.Parse(root)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RequestURI() != "/" || u.Fragment != "" {
		return nil, errors.New("verified HTTPS admin origin required")
	}
	cookie, err := c.Cookie()
	if err != nil {
		return nil, err
	}
	client := *hc
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = 15 * time.Second
	return &Actor{strings.TrimSuffix(root, "/"), cookie, &client}, nil
}
func (a *Actor) call(ctx context.Context, method, path string, body, out any) (int, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.root+path, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Cookie", a.cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, errors.New("operator request transport outcome unknown")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<18)+1))
	if err != nil || len(raw) > 1<<18 || json.Unmarshal(raw, out) != nil {
		return resp.StatusCode, errors.New("operator response incomplete")
	}
	return resp.StatusCode, nil
}
func (a *Actor) Eligibility(ctx context.Context, id string) (Eligibility, error) {
	var zero Eligibility
	if _, err := uuid.Parse(id); err != nil {
		return zero, err
	}
	var res struct{ Items []Eligibility }
	status, err := a.call(ctx, "POST", "/api/alertmanager/action-eligibility", map[string]any{"history_ids": []string{id}}, &res)
	if err != nil || status != 200 || len(res.Items) != 1 || res.Items[0].HistoryID != id || res.Items[0].Episode < 1 {
		return zero, errors.New("signed eligibility unavailable for selected episode")
	}
	item := res.Items[0]
	actions := map[string]Decision{}
	for _, key := range []string{"resend", "ticket_create", "acknowledge", "clear", "escalate", "ticket_associate"} {
		if decision, ok := item.Actions[key]; ok {
			actions[key] = decision
		}
	}
	item.Actions = actions
	return item, nil
}

// Command sends one intent; a transport error never allocates a replacement ID.
func (a *Actor) Command(ctx context.Context, kind, id, request string) (CommandReceipt, error) {
	var r CommandReceipt
	if _, err := uuid.Parse(id); err != nil {
		return r, err
	}
	if _, err := uuid.Parse(request); err != nil {
		return r, err
	}
	body := map[string]any{"request_id": request, "confirmed": true}
	path := ""
	switch kind {
	case "manual_resend":
		path = "/api/alertmanager/resend/" + id
		body["comment"] = "CorrTest managed episode"
	case "acknowledge":
		path = "/api/alertmanager/acknowledge"
		body["id"] = id
	case "manual_ticket":
		path = "/api/alertmanager/history/ticket"
		body["alertId"] = id
		body["note"] = "CorrTest managed episode"
	default:
		return r, errors.New("unsupported operator action")
	}
	status, err := a.call(ctx, "POST", path, body, &r)
	r.StatusCode = status
	return r, err
}
func (a *Actor) ResendStatus(ctx context.Context, id, op string) (CommandReceipt, error) {
	var r CommandReceipt
	for _, s := range []string{id, op} {
		if _, err := uuid.Parse(s); err != nil {
			return r, err
		}
	}
	status, err := a.call(ctx, "GET", "/api/alertmanager/resend/"+id+"/"+op, nil, &r)
	r.StatusCode = status
	if err != nil || status != 200 || r.AlertID != id || r.ResendID != op {
		return r, errors.New("original resend receipt unavailable")
	}
	return r, nil
}
func (a *Actor) Session(ctx context.Context) error {
	var s struct {
		User    *struct{ Name string }
		Expires string
	}
	status, err := a.call(ctx, "GET", "/api/auth/session", nil, &s)
	if err != nil || status != 200 || s.User == nil {
		return errors.New("disposable operator session unavailable")
	}
	return nil
}

// Bulk preserves one request ID per selected episode, including a refused row.
func (a *Actor) Bulk(ctx context.Context, ids, requests []string) (CommandReceipt, error) {
	var r CommandReceipt
	if len(ids) != 2 || len(requests) != 2 || ids[0] == ids[1] || requests[0] == requests[1] {
		return r, errors.New("two distinct owned rows required")
	}
	items := make([]map[string]string, 2)
	for i, id := range ids {
		if _, e := uuid.Parse(id); e != nil {
			return r, e
		}
		if _, e := uuid.Parse(requests[i]); e != nil {
			return r, e
		}
		items[i] = map[string]string{"alert_id": id, "request_id": requests[i]}
	}
	status, err := a.call(ctx, "POST", "/api/alertmanager/resend-bulk", map[string]any{"confirmed": true, "comment": "CorrTest mixed eligibility", "items": items}, &r)
	r.StatusCode = status
	return r, err
}
