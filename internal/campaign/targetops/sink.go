package targetops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Sink struct {
	base  string // https://load.home.arpa:18090
	token string
	hc    *http.Client
}

type SinkAlert struct {
	HistoryID string            `json:"history_id"`
	StartsAt  string            `json:"starts_at"`
	AlertName string            `json:"alertname"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
}

type Receipt struct {
	ID         int64       `json:"receipt_id"`
	Notifier   string      `json:"notifier"`
	ReceivedAt time.Time   `json:"received_at"`
	Alerts     []SinkAlert `json:"alerts"`
}

func NewSink(baseURL, tokenFile, caFile string) (*Sink, error) {
	hc, err := newHTTP(caFile, 30*time.Second)
	if err != nil {
		return nil, err
	}
	tok, err := readSecret(tokenFile)
	if err != nil {
		return nil, err
	}
	return &Sink{base: strings.TrimRight(baseURL, "/"), token: tok, hc: hc}, nil
}

// Receipts pages the capture from since (RFC 3339, ascending, by after_id) and keeps only alerts whose
// campaign_run label equals campaignRun; receipts with no matching alert are dropped. The sink's own run filter is not
// used: it only knows a few label names and the capture URL's ?run=, never campaign_run. A zero since means no lower bound.
func (s *Sink) Receipts(ctx context.Context, capture, campaignRun string, since time.Time) ([]Receipt, error) {
	return s.receiptsByLabel(ctx, capture, "campaign_run", campaignRun, since)
}

func (s *Sink) ManagedReceipts(ctx context.Context, aid string, since time.Time) ([]Receipt, error) {
	return s.receiptsByLabel(ctx, "campaign", "aid", aid, since)
}

func (s *Sink) receiptsByLabel(ctx context.Context, capture, label, value string, since time.Time) ([]Receipt, error) {
	if value == "" {
		return nil, fmt.Errorf("campaign run is required")
	}
	var all []Receipt
	after := int64(0)
	for page := 0; page < 1000; page++ {
		q := url.Values{"capture": {capture}, "order": {"asc"}, "limit": {"1000"}}
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}
		if after > 0 {
			q.Set("after_id", strconv.FormatInt(after, 10))
		}
		status, raw, err := doRaw(ctx, s.hc, http.MethodGet, s.base+"/api/v1/receipts?"+q.Encode(), nil, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+s.token)
		})
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("sink receipts returned HTTP %d", status)
		}
		var resp struct {
			Receipts    []Receipt `json:"receipts"`
			NextAfterID int64     `json:"next_after_id"`
		}
		if json.Unmarshal(raw, &resp) != nil {
			return nil, fmt.Errorf("sink returned malformed JSON")
		}
		for _, r := range resp.Receipts {
			var mine []SinkAlert
			for _, a := range r.Alerts {
				if a.Labels[label] == value {
					mine = append(mine, a)
				}
			}
			if len(mine) > 0 {
				r.Alerts = mine
				all = append(all, r)
			}
		}
		if resp.NextAfterID == 0 {
			return all, nil
		}
		after = resp.NextAfterID
	}
	return nil, fmt.Errorf("sink receipts exceeded page limit")
}
