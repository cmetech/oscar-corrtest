package frontoffice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
)

type fakeClock struct {
	mu         sync.Mutex
	now, limit time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) SleepUntil(ctx context.Context, t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.limit) || ctx.Err() != nil {
		return false
	}
	if t.After(c.now) {
		c.now = t
	}
	return true
}

var spec = scenario.FrontOfficeSpec{Users: 4, Interval: 15 * time.Second, Jitter: 3 * time.Second, SearchEvery: 2 * time.Minute}

func replay(t *testing.T, h http.HandlerFunc, users []User, d time.Duration) []ActionResult {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	t0 := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	var res []ActionResult
	Run(context.Background(), srv.URL, srv.Client(), users, 7, spec, &fakeClock{now: t0, limit: t0.Add(d)}, func(r ActionResult) { res = append(res, r) })
	return res
}

func ok(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }

func TestEachUserUsesOwnKey(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	users := []User{{"fo-01", "k1"}, {"fo-02", "k2"}}
	replay(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get("X-API-Key")]++
		mu.Unlock()
		ok(w, r)
	}, users, 2*time.Minute)
	if seen["k1"] == 0 || seen["k2"] == 0 || len(seen) != 2 {
		t.Fatalf("keys: %v", seen)
	}
}

func TestSearchAtMostOncePerTwoMinutesPerUser(t *testing.T) {
	res := replay(t, ok, []User{{"fo-01", "k1"}, {"fo-02", "k2"}}, 10*time.Minute)
	n := map[string]int{}
	for _, r := range res {
		if r.Action == "search" {
			n[r.User]++
		}
	}
	for u, c := range n {
		if c > 5 || c == 0 {
			t.Fatalf("%s searches=%d", u, c)
		}
	}
}

func TestSeededSequenceDeterministic(t *testing.T) {
	seq := func() map[string][]string {
		m := map[string][]string{}
		for _, r := range replay(t, ok, []User{{"fo-01", "k1"}, {"fo-02", "k2"}}, 20*time.Minute) {
			m[r.User] = append(m[r.User], r.Action)
		}
		return m
	}
	a, b := seq(), seq()
	for u := range a {
		if !slices.Equal(a[u], b[u]) {
			t.Fatalf("%s differs", u)
		}
	}
	if slices.Equal(a["fo-01"], a["fo-02"]) {
		t.Fatal("users should differ")
	}
}

func TestKeysNeverInResults(t *testing.T) {
	res := replay(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad "+r.Header.Get("X-API-Key"), 500) },
		[]User{{"fo-01", "SECRETKEY"}}, time.Minute)
	if len(res) < 3 {
		t.Fatalf("loop should continue after errors: %d", len(res))
	}
	for _, r := range res {
		if r.Err == "" || r.Status != 500 || strings.Contains(r.Err+r.User+r.Action, "SECRETKEY") {
			t.Fatalf("bad result %+v", r)
		}
	}
}

func TestActionsMatchScorecard(t *testing.T) {
	got := map[string]bool{}
	for _, r := range replay(t, ok, []User{{"fo-01", "k"}}, 2*time.Hour) {
		if !slices.Contains(score.FOActions, r.Action) {
			t.Fatalf("unknown action %s", r.Action)
		}
		got[r.Action] = true
	}
	for _, a := range score.FOActions {
		if !got[a] {
			t.Fatalf("never emitted %s", a)
		}
	}
}

func TestLoadUsers(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "fo-01.json"), []byte(`{"api_key":"a","other":"x"}`), 0o600)
	os.WriteFile(filepath.Join(d, "fo-02.json"), []byte(`{"api_key":"b"}`), 0o600)
	u, err := LoadUsers(d, 2)
	if err != nil || len(u) != 2 || u[1] != (User{"fo-02", "b"}) {
		t.Fatalf("%v %v", u, err)
	}
	if _, err := LoadUsers(d, 3); err == nil {
		t.Fatal("want error for missing user")
	}
}
