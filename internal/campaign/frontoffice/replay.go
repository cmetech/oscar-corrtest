// Package frontoffice replays a seeded loop of Alerts-page API requests per
// front-office user and records latency per action.
package frontoffice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
)

type User struct{ Name, APIKey string }

type ActionResult struct {
	User, Action  string
	At            time.Time
	Status        int
	LatencyMillis int64
	Err           string        // error class only; never contains the key
	Source        *SourceResult // source actions only, on a 2xx answer
}

// Sources (3ps v3, OSC-207) adds the Alert View Source / Source IP filter
// actions (score.SourceActions). Since is the window start on
// last_occurrence: T0, the start of the campaign's own History read-back,
// so a source_equals count and that read-back cover the same rows; Worker is
// the worker lane's server count (source_equals and instance_filter pick one).
type Sources struct {
	Since  time.Time
	Worker int
}

// SourceResult is a source filter answer: its value, total_records, the rows
// on the page, those whose derived Source / Source IP misses the filter, those
// without labels (not checkable), and when the answer was complete.
type SourceResult struct {
	Value                               string
	Count, Rows, Mismatch, Unverifiable int
	End                                 time.Time
}

// SourceFragments are the source_contains terms: each matches 9 or 10 of
// ct-srv-01..40.
var SourceFragments = []string{"srv-0", "srv-1", "srv-2", "srv-3"}

// SourceIPPrefix is the source_ip_startswith term: servers 1-9.
const SourceIPPrefix = "198.51.100.1"

// LoadUsers reads dir/fo-01.json .. fo-0N.json, using api_key only.
func LoadUsers(dir string, n int) ([]User, error) {
	var us []User
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("fo-%02d", i)
		b, err := os.ReadFile(filepath.Join(dir, name+".json")) // #nosec G304 -- operator-configured front-office directory plus a fixed fo-NN name.
		if err != nil {
			return nil, err
		}
		var f struct {
			APIKey string `json:"api_key"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("%s: invalid json", name) // no wrapped err: may echo content
		}
		if f.APIKey == "" {
			return nil, fmt.Errorf("%s: missing api_key", name)
		}
		us = append(us, User{name, f.APIKey})
	}
	return us, nil
}

var (
	severities = []string{"critical", "warning", "info"}
	// every scorecard action except search, which is scheduled by SearchEvery.
	rotating = slices.DeleteFunc(slices.Clone(score.FOActions), func(a string) bool { return a == "search" })
)

// Run blocks until ctx is cancelled or the clock stops. Calls to out are serialized.
func Run(ctx context.Context, base string, client *http.Client, users []User, instances int, seed int64, spec scenario.FrontOfficeSpec, src *Sources, clock lane.Clock, out func(ActionResult)) {
	var mu sync.Mutex
	emit := func(r ActionResult) { mu.Lock(); defer mu.Unlock(); out(r) }
	// One start for every user: each schedule is a function of (seed, start,
	// spec) alone, not of when its goroutine first ran (a shared virtual clock
	// may already have been advanced by another user by then).
	start := clock.Now()
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runUser(ctx, base, client, u, start, rand.New(rand.NewSource(seed+int64(i)*7919)) /* #nosec G404 -- deterministic seeded replay; not security-sensitive */, instances, spec, src, clock, emit)
		}()
	}
	wg.Wait()
}

func runUser(ctx context.Context, base string, client *http.Client, u User, start time.Time, rng *rand.Rand, instances int, spec scenario.FrontOfficeSpec, src *Sources, clock lane.Clock, emit func(ActionResult)) {
	actions := rotating
	if src != nil {
		actions = append(slices.Clone(rotating), score.SourceActions...)
	}
	due := start
	nextSearch := due
	for ctx.Err() == nil && clock.SleepUntil(ctx, due) {
		var action string
		if !due.Before(nextSearch) {
			action, nextSearch = "search", due.Add(spec.SearchEvery)
		} else {
			action = actions[rng.Intn(len(actions))]
		}
		// paging contributes two samples (pages 2 and 3).
		qs, value := queries(action, rng, instances, due), ""
		if src != nil {
			qs, value = src.queries(action, rng, due, qs)
		}
		for _, q := range qs {
			r := call(ctx, client, base, u, action, value, q, clock)
			if ctx.Err() != nil {
				return // cancelled mid-request: not a real sample
			}
			emit(r)
		}
		due = due.Add(spec.Interval)
		if spec.Jitter > 0 {
			due = due.Add(time.Duration(rng.Int63n(int64(2*spec.Jitter+1))) - spec.Jitter)
		}
	}
}

// queries returns "path?query" strings for the action (paging makes two).
func queries(action string, rng *rand.Rand, instances int, now time.Time) []string {
	const hist = "/alerts/history"
	h := func(kv ...string) string {
		v := url.Values{"page": {"1"}, "perPage": {"50"}, "view_status": {"firing"}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return hist + "?" + v.Encode()
	}
	switch action {
	case "firing_page1":
		return []string{h()}
	case "severity_filter":
		return []string{h("view_severity", severities[rng.Intn(len(severities))])}
	case "status_filter":
		return []string{h("view_status", "resolved")}
	case "paging":
		return []string{h("page", "2"), h("page", "3")}
	case "instance_filter":
		f, _ := json.Marshal(map[string]any{"items": []map[string]string{{"field": "label:instance", "operator": "equals", "value": lane.InstanceName(rng.Intn(instances) + 1)}}, "logicOperator": "and"})
		return []string{h("filter", string(f))}
	case "search":
		v := url.Values{"q": {"Campaign"}, "limit": {"50"}, "search_mode": {"fulltext"},
			"start_datetime": {now.Add(-time.Hour).UTC().Format(time.RFC3339)}, "end_datetime": {now.UTC().Format(time.RFC3339)}}
		return []string{hist + "/search?" + v.Encode()}
	default: // active_alerts
		return []string{"/alerts/active-alerts?page=1&perPage=50"}
	}
}

// queries replaces the instance_filter value with a worker server's instance
// and builds the source actions' uisrc queries, returning the filter value.
func (s *Sources) queries(action string, rng *rand.Rand, now time.Time, qs []string) ([]string, string) {
	value := ""
	switch action {
	case "instance_filter":
		value = lane.ServerInstance(rng.Intn(s.Worker) + 1)
		f, _ := json.Marshal(map[string]any{"items": []map[string]string{{"field": "label:instance", "operator": "equals", "value": value}}, "logicOperator": "and"})
		v := url.Values{"page": {"1"}, "perPage": {"50"}, "view_status": {"firing"}, "filter": {string(f)}}
		return []string{"/alerts/history?" + v.Encode()}, value
	case "source_equals":
		value = lane.ServerName(rng.Intn(s.Worker) + 1)
	case "source_contains":
		value = SourceFragments[rng.Intn(len(SourceFragments))]
	case "source_ip_startswith":
		value = SourceIPPrefix
	case "source_ip_notempty":
	default:
		return qs, ""
	}
	field, op := score.SourceFilter(action)
	item := map[string]any{"field": field, "operator": op}
	if op != "isNotEmpty" {
		item["value"] = value
	}
	f, _ := json.Marshal(map[string]any{"items": []any{item}, "logicOperator": "and"})
	v := url.Values{"page": {"1"}, "perPage": {"50"}, "filter": {string(f)},
		"start_datetime": {s.Since.UTC().Format(time.RFC3339Nano)}, "end_datetime": {now.Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)}}
	return []string{"/alerts/history?" + v.Encode()}, value
}

// sourceAnswer checks a source action's page: every row's derived value must
// match the filter.
func sourceAnswer(action, value string, body io.Reader) (*SourceResult, error) {
	var page struct {
		Total   int `json:"total_records"`
		Records []struct {
			Labels []struct{ Label, Value string } `json:"labels"`
		} `json:"records"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 8<<20)).Decode(&page); err != nil {
		return nil, err
	}
	res := &SourceResult{Value: value, Count: page.Total, Rows: len(page.Records)}
	for _, rec := range page.Records {
		labels := map[string]string{}
		for _, l := range rec.Labels {
			if _, dup := labels[l.Label]; !dup { // OSCAR derives from the first of a name
				labels[l.Label] = l.Value
			}
		}
		switch {
		case len(labels) == 0: // withheld labels (a correlation parent): nothing to derive from
			res.Unverifiable++
		case !score.MatchesSourceFilter(action, value, labels):
			res.Mismatch++
		}
	}
	return res, nil
}

func call(ctx context.Context, client *http.Client, base string, u User, action, value, q string, clock lane.Clock) ActionResult {
	res := ActionResult{User: u.Name, Action: action, At: clock.Now()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ext/mw/api/v1"+q, nil)
	if err != nil {
		res.Err = "request"
		return res
	}
	req.Header.Set("X-API-Key", u.APIKey)
	start := time.Now()
	resp, err := client.Do(req)
	var parseErr error
	if err == nil {
		res.Status = resp.StatusCode
		if res.Status/100 == 2 && slices.Contains(score.SourceActions, action) {
			res.Source, parseErr = sourceAnswer(action, value, resp.Body)
		}
		_, _ = io.Copy(io.Discard, resp.Body) // best-effort drain so the connection can be reused
		_ = resp.Body.Close()                 // read-only response; close error is not actionable
	}
	// latency is wall time, independent of the injected clock.
	res.LatencyMillis = time.Since(start).Milliseconds()
	if res.Source != nil {
		res.Source.End = clock.Now()
	}
	switch {
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)):
		res.Err = "timeout"
	case err != nil:
		res.Err = "transport"
	case res.Status >= 500:
		res.Err = "http_5xx"
	case res.Status >= 400:
		res.Err = "http_4xx"
	case res.Status < 200 || res.Status > 299:
		res.Err = "http_other"
	case parseErr != nil:
		res.Err = "decode"
	}
	return res
}
