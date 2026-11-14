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
	AlertName string            `json:"alertname"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
}

type Receipt struct {
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

// Receipts returns all receipts for the capture and campaign run (the sink's run filter), ascending, paging by after_id.
func (s *Sink) Receipts(ctx context.Context, capture, campaignRun string) ([]Receipt, error) {
	var all []Receipt
	after := int64(0)
	for page := 0; page < 1000; page++ {
		q := url.Values{"capture": {capture}, "run": {campaignRun}, "order": {"asc"}, "limit": {"1000"}}
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
		all = append(all, resp.Receipts...)
		if resp.NextAfterID == 0 {
			return all, nil
		}
		after = resp.NextAfterID
	}
	return nil, fmt.Errorf("sink receipts exceeded page limit")
}
