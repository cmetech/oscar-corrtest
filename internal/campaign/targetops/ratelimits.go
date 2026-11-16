package targetops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

const alertLimitsPath = "/api/v1/alerts/ratelimits"

// AlertLimits is the mutable part of OSCAR's alert ingest rate-limit document:
// the revision a PUT must name, and the whole overrides object (a PUT replaces
// it, so a restore puts back exactly what was read).
type AlertLimits struct {
	Revision  int             `json:"revision"`
	Overrides json.RawMessage `json:"overrides"`
}

// GetAlertLimits reads GET /api/v1/alerts/ratelimits.
func GetAlertLimits(ctx context.Context, c *oscar.Client) (AlertLimits, error) {
	var out AlertLimits
	if err := c.DoJSON(ctx, http.MethodGet, alertLimitsPath, nil, nil, &out); err != nil {
		return AlertLimits{}, err
	}
	if len(out.Overrides) == 0 {
		return AlertLimits{}, fmt.Errorf("alert rate limits: no overrides object")
	}
	return out, nil
}

// PutAlertLimits replaces the overrides at revision rev and reads the document back.
func PutAlertLimits(ctx context.Context, c *oscar.Client, rev int, overrides json.RawMessage) (AlertLimits, error) {
	body := map[string]any{"revision": rev, "overrides": overrides}
	if err := c.DoJSON(ctx, http.MethodPut, alertLimitsPath, nil, body, nil); err != nil {
		return AlertLimits{}, err
	}
	return GetAlertLimits(ctx, c)
}

// WithQueue returns overrides with the queue (circuit breaker) entry replaced;
// every other override is kept as read.
func WithQueue(overrides json.RawMessage, high, low int) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(overrides, &m); err != nil {
		return nil, fmt.Errorf("alert rate limit overrides: %w", err)
	}
	q, _ := json.Marshal(map[string]any{"enabled": true, "high": high, "low": low})
	m["queue"] = q
	return json.Marshal(m)
}

// SameOverrides reports whether two overrides objects are equal as JSON.
func SameOverrides(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x) // maps marshal with sorted keys
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}
