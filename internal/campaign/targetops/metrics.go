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

type Metrics struct {
	base   string // e.g. https://oscar.home.arpa/ext/vmdb
	bearer string
	hc     *http.Client
}

type Sample struct {
	Metric map[string]string
	Time   float64
	Value  float64
}

type Vector []Sample

type Series struct {
	Metric map[string]string
	Values []Point
}

type Point struct {
	Time  float64
	Value float64
}

type Matrix []Series

// NewMetrics: baseURL is the VictoriaMetrics root (target scheme+host + /ext/vmdb); bearerFile is optional.
func NewMetrics(baseURL, caFile, bearerFile string) (*Metrics, error) {
	hc, err := newHTTP(caFile, 60*time.Second)
	if err != nil {
		return nil, err
	}
	m := &Metrics{base: strings.TrimRight(baseURL, "/"), hc: hc}
	if bearerFile != "" {
		if m.bearer, err = readSecret(bearerFile); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Metrics) get(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	status, raw, err := doRaw(ctx, m.hc, http.MethodGet, m.base+path+"?"+q.Encode(), nil, func(r *http.Request) {
		if m.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+m.bearer)
		}
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("metrics %s returned HTTP %d", path, status)
	}
	var env struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Status != "success" {
		return nil, fmt.Errorf("metrics %s returned an unsuccessful response", path)
	}
	return env.Data, nil
}

func ts(t time.Time) string { return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64) }

type promPoint [2]json.RawMessage

func (p promPoint) point() (Point, error) {
	var t float64
	var s string
	if json.Unmarshal(p[0], &t) != nil || json.Unmarshal(p[1], &s) != nil {
		return Point{}, fmt.Errorf("malformed sample")
	}
	v, err := strconv.ParseFloat(s, 64)
	return Point{Time: t, Value: v}, err
}

func (m *Metrics) Query(ctx context.Context, promql string, at time.Time) (Vector, error) {
	data, err := m.get(ctx, "/api/v1/query", url.Values{"query": {promql}, "time": {ts(at)}})
	if err != nil {
		return nil, err
	}
	var d struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  promPoint         `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("malformed query result")
	}
	out := make(Vector, 0, len(d.Result))
	for _, r := range d.Result {
		p, err := r.Value.point()
		if err != nil {
			return nil, err
		}
		out = append(out, Sample{Metric: r.Metric, Time: p.Time, Value: p.Value})
	}
	return out, nil
}

func (m *Metrics) rangeRaw(ctx context.Context, promql string, start, end time.Time, step time.Duration) (json.RawMessage, error) {
	return m.get(ctx, "/api/v1/query_range", url.Values{"query": {promql}, "start": {ts(start)}, "end": {ts(end)},
		"step": {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)}})
}

func (m *Metrics) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (Matrix, error) {
	data, err := m.rangeRaw(ctx, promql, start, end, step)
	if err != nil {
		return nil, err
	}
	var d struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Values []promPoint       `json:"values"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("malformed range result")
	}
	out := make(Matrix, 0, len(d.Result))
	for _, r := range d.Result {
		s := Series{Metric: r.Metric}
		for _, p := range r.Values {
			pt, err := p.point()
			if err != nil {
				return nil, err
			}
			s.Values = append(s.Values, pt)
		}
		out = append(out, s)
	}
	return out, nil
}

// Snapshot runs every named query over the window and returns {"name": <prometheus data>, ...}.
// It fails if any query fails, so a snapshot never silently omits a query.
func (m *Metrics) Snapshot(ctx context.Context, queries map[string]string, start, end time.Time, step time.Duration) (json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(queries))
	for name, q := range queries {
		data, err := m.rangeRaw(ctx, q, start, end, step)
		if err != nil {
			return nil, fmt.Errorf("snapshot query %q: %w", name, err)
		}
		out[name] = data
	}
	return json.Marshal(out)
}
