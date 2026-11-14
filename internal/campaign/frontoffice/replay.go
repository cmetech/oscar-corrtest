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
	Err           string // error class only; never contains the key
}

// LoadUsers reads dir/fo-01.json .. fo-0N.json, using api_key only.
func LoadUsers(dir string, n int) ([]User, error) {
	var us []User
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("fo-%02d", i)
		b, err := os.ReadFile(filepath.Join(dir, name+".json"))
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
func Run(ctx context.Context, base string, client *http.Client, users []User, instances int, seed int64, spec scenario.FrontOfficeSpec, clock lane.Clock, out func(ActionResult)) {
	var mu sync.Mutex
	emit := func(r ActionResult) { mu.Lock(); defer mu.Unlock(); out(r) }
	var wg sync.WaitGroup
	for i, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runUser(ctx, base, client, u, rand.New(rand.NewSource(seed+int64(i)*7919)), instances, spec, clock, emit)
		}()
	}
	wg.Wait()
}

func runUser(ctx context.Context, base string, client *http.Client, u User, rng *rand.Rand, instances int, spec scenario.FrontOfficeSpec, clock lane.Clock, emit func(ActionResult)) {
	due := clock.Now()
	nextSearch := due
	for ctx.Err() == nil && clock.SleepUntil(ctx, due) {
		var action string
		if !due.Before(nextSearch) {
			action, nextSearch = "search", due.Add(spec.SearchEvery)
		} else {
			action = rotating[rng.Intn(len(rotating))]
		}
		// paging contributes two samples (pages 2 and 3).
		for _, q := range queries(action, rng, instances, due) {
			r := call(ctx, client, base, u, action, q, clock)
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
		for i := 0; i < len(kv); i += 2 {
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

func call(ctx context.Context, client *http.Client, base string, u User, action, q string, clock lane.Clock) ActionResult {
	res := ActionResult{User: u.Name, Action: action, At: clock.Now()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ext/mw/api/v1"+q, nil)
	if err != nil {
		res.Err = "request"
		return res
	}
	req.Header.Set("X-API-Key", u.APIKey)
	start := time.Now()
	resp, err := client.Do(req)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		res.Status = resp.StatusCode
	}
	// latency is wall time, independent of the injected clock.
	res.LatencyMillis = time.Since(start).Milliseconds()
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
	}
	return res
}
