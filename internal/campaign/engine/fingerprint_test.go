package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func fingerprintHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.s.Fingerprint = &scenario.FingerprintSpec{At: 10 * time.Second, Repeats: 3, Converge: 5 * time.Second, Verify: 10 * time.Second}
	h.oscar.s = h.s
	return h
}

func fingerprintCard(t *testing.T, c domain.Campaign) (verdict domain.Verdict, reasons []string, fp *score.FingerprintStats) {
	t.Helper()
	var card struct {
		Verdict     domain.Verdict
		Reasons     []string
		Fingerprint *score.FingerprintStats
	}
	if err := json.Unmarshal(c.Scorecard, &card); err != nil || card.Fingerprint == nil {
		t.Fatalf("scorecard fingerprint section: %v (%s)", err, c.Scorecard)
	}
	return card.Verdict, card.Reasons, card.Fingerprint
}

func fingerprintReasons(reasons []string) []string {
	var out []string
	for _, r := range reasons {
		if strings.Contains(r, "fingerprint") {
			out = append(out, r)
		}
	}
	return out
}

// TestFingerprintLaneMergesSplitsAndReverts: with the policy served, repeats
// differing only in the ignored label are one History row, the device control
// is another, every row carries the publication, and the policy is reverted.
// A target that ignores the policy's exclusion fails the merge.
func TestFingerprintLaneMergesSplitsAndReverts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		noMerge  bool
		wantFail string
	}{{name: "merged"}, {name: "not merged", noMerge: true, wantFail: "fingerprint merge: 3 History rows"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fingerprintHarness(t)
			h.oscar.fpNoMerge = tc.noMerge
			if err := h.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			c := h.campaign()
			verdict, reasons, fp := fingerprintCard(t, c)
			if c.Cleanup != domain.CleanupClean || !fp.Reverted || fp.PublicationID != "pub-1" || fp.Sent != 4 {
				t.Fatalf("cleanup %s stats %+v", c.Cleanup, fp)
			}
			h.assertRestored()
			h.oscar.mu.Lock()
			on, enables, disables := h.oscar.fpOn, h.oscar.fpEnables, h.oscar.fpDisables
			h.oscar.mu.Unlock()
			if on || enables != 1 || disables != 1 {
				t.Fatalf("policy on=%t enables %d disables %d", on, enables, disables)
			}
			got := fingerprintReasons(reasons)
			if tc.wantFail == "" {
				if len(got) > 0 || fp.MergedRows != 1 || fp.SplitRows != 1 {
					t.Fatalf("reasons %v stats %+v", got, fp)
				}
				return
			}
			if verdict != domain.VerdictFail || len(got) != 1 || !strings.HasPrefix(got[0], tc.wantFail) {
				t.Fatalf("verdict %s reasons %v", verdict, got)
			}
		})
	}
}

// TestFingerprintLaneSkippedWhenNotCommissioned: the lane changes nothing,
// sends nothing and scores neither PASS nor FAIL.
func TestFingerprintLaneSkippedWhenNotCommissioned(t *testing.T) {
	t.Parallel()
	h := fingerprintHarness(t)
	h.oscar.fpSkip = "fingerprint policies unpublished (no desired publication)"
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := h.campaign()
	_, reasons, fp := fingerprintCard(t, c)
	if fp.Skipped != h.oscar.fpSkip || len(fingerprintReasons(reasons)) > 0 || c.Cleanup != domain.CleanupClean {
		t.Fatalf("stats %+v reasons %v cleanup %s", fp, reasons, c.Cleanup)
	}
	h.oscar.mu.Lock()
	defer h.oscar.mu.Unlock()
	if h.oscar.fpEnables+h.oscar.fpDisables != 0 || h.oscar.posts[fingerprintAlert] != 0 {
		t.Fatalf("skipped lane acted: enables %d disables %d posts %d", h.oscar.fpEnables, h.oscar.fpDisables, h.oscar.posts[fingerprintAlert])
	}
}

// TestFingerprintRevertFailureIsDirty: a disable that never converges leaves
// the mutation open, so cleanup is DIRTY rather than silently CLEAN.
func TestFingerprintRevertFailureIsDirty(t *testing.T) {
	t.Parallel()
	h := fingerprintHarness(t)
	h.oscar.fpDisableErr = errors.New("fingerprint publication did not converge in time")
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := h.campaign()
	_, _, fp := fingerprintCard(t, c)
	if c.Cleanup != domain.CleanupDirty || fp.Reverted {
		t.Fatalf("cleanup %s reverted %t", c.Cleanup, fp.Reverted)
	}
	open, err := h.db.OpenMutations(context.Background(), h.id)
	if err != nil || len(open) != 1 || open[0].Kind != kindFingerprintPolicy {
		t.Fatalf("open mutations %v %+v", err, open)
	}
	h.oscar.mu.Lock()
	h.oscar.fpDisableErr = nil
	h.oscar.mu.Unlock()
	if st, err := New(h.deps, h.clock).Cleanup(context.Background(), h.id); err != nil || st != domain.CleanupClean {
		t.Fatalf("cleanup re-run: %s %v", st, err)
	}
}

// TestFingerprintCleanupResolvesRevertedRows: the live state of
// camp_19782e09e46265eb, where the policy was reverted while its merged rows
// still fired. A resolve then no longer matches them, so a cleanup re-run
// re-enables the policy, resolves under it and reverts again; when the rows
// still cannot be resolved it stays DIRTY and says why.
func TestFingerprintCleanupResolvesRevertedRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ignore bool
		want   domain.CleanupStatus
	}{{name: "re-enabled and resolved", want: domain.CleanupClean}, {name: "resolve refused", ignore: true, want: domain.CleanupDirty}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := fingerprintHarness(t)
			if err := h.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if c := h.campaign(); c.Cleanup != domain.CleanupClean {
				t.Fatalf("first cleanup %s", c.Cleanup)
			}
			h.oscar.mu.Lock()
			reopened := 0
			for row := range h.oscar.fpScoped {
				row.Status = "firing"
				reopened++
			}
			h.oscar.ignoreResolves = tc.ignore
			h.oscar.mu.Unlock()
			if reopened != 2 {
				t.Fatalf("policy-scoped rows %d, want 2", reopened)
			}
			st, err := New(h.deps, h.clock).Cleanup(context.Background(), h.id)
			if err != nil || st != tc.want {
				t.Fatalf("cleanup re-run: %s %v", st, err)
			}
			h.oscar.mu.Lock()
			on, enables, disables := h.oscar.fpOn, h.oscar.fpEnables, h.oscar.fpDisables
			h.oscar.mu.Unlock()
			if tc.want == domain.CleanupClean {
				if on || enables != 2 || disables != 2 {
					t.Fatalf("policy on=%t enables %d disables %d", on, enables, disables)
				}
				h.assertRestored()
				return
			}
			// Unresolved rows keep the policy on (the only state they resolve
			// in) with its mutation open for the next cleanup.
			open, err := h.db.OpenMutations(context.Background(), h.id)
			if !on || enables != 2 || disables != 1 || err != nil || len(open) != 1 || open[0].Kind != kindFingerprintPolicy {
				t.Fatalf("policy on=%t enables %d disables %d open %v %+v", on, enables, disables, err, open)
			}
			evs, _ := h.db.RecentCampaignEvents(context.Background(), h.id, 50)
			for _, e := range evs {
				if strings.Contains(e.Summary, fingerprintAlert+" alerts fired under the fingerprint policy") && strings.Contains(e.Summary, "history ") {
					return
				}
			}
			t.Fatalf("no precise fingerprint DIRTY reason in %+v", evs)
		})
	}
}
