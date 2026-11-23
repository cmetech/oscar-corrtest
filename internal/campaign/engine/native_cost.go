package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// A complete, independently collected trace for each observed physical
// request. SQL and HTTP spans use an allowlisted category, never raw text.
type traceSpan struct {
	Category             string
	OffsetNS, DurationNS int64
}
type attemptTrace struct {
	TraceID, RequestID         string
	Started, Finished          time.Time
	SQLSeen, HTTPSeen, Dropped *int
	Spans                      []traceSpan
}
type costFile struct {
	Run, Hash, TargetBuild, CorrTestBuild, WorkloadConfig, ObserverConfig string
	From, Through                                                         time.Time
	SamplingFraction, ObserverCPUSeconds                                  *float64
	Routes                                                                map[string]captureRoute
	Decisions                                                             []routeDecision
	Requests                                                              []capturedRequest
	Attempts                                                              []attemptTrace
}
type costStats struct {
	CaptureSHA256, ObserverConfig, ComparisonConfig string
	Outcomes                                        map[string]int
	PhysicalAttempts                                int
	ObserverCPUSeconds                              float64
	Attempts                                        map[string]int
	Counts                                          map[string]map[string]int
	Seconds                                         map[string]map[string]float64
	Headlines                                       []score.Headline
}

func (r *run) costCapture(t *nativeTally, rs []targetops.Receipt, end time.Time) (costStats, error) {
	var out costStats
	if r.e.d.ObserverDir == "" {
		return out, errors.New("ordinary capture directory unavailable")
	}
	root, err := os.OpenRoot(r.e.d.ObserverDir)
	if err != nil {
		return out, errors.New("cannot open observer evidence directory")
	}
	defer root.Close()
	if filepath.Base(r.id) != r.id {
		return out, errors.New("invalid run ID")
	}
	st, err := root.Lstat(r.id + ".json")
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16<<20 {
		return out, errors.New("per-run regular trace capture absent or oversized")
	}
	f, err := root.Open(r.id + ".json")
	if err != nil {
		return out, errors.New("per-run trace capture absent")
	}
	defer f.Close()
	const limit = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) > limit {
		return out, errors.New("unreadable or oversized trace capture")
	}
	var c costFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF {
		return out, errors.New("invalid trace capture format")
	}
	p := r.opts.Provenance
	if c.Run != r.id || c.Hash != r.s.Hash() || !digest(c.ObserverConfig, 32) || c.From.IsZero() || c.From.After(r.t0) || c.Through.Before(end) || end.Before(r.t0.Add(r.s.LoadEnd)) || c.SamplingFraction == nil || *c.SamplingFraction != 1 {
		return out, errors.New("capture run, hash, observed window or lossless coverage incomplete")
	}
	sum := sha256.Sum256(raw)
	out = costStats{CaptureSHA256: hex.EncodeToString(sum[:]), ObserverConfig: c.ObserverConfig, Outcomes: map[string]int{}, Attempts: map[string]int{}, Counts: map[string]map[string]int{}, Seconds: map[string]map[string]float64{}}
	out.ComparisonConfig = configurationDigest(c.ObserverConfig, c.Routes)
	contacts, err := r.ordinaryContacts(t, c, rs)
	if err != nil {
		return out, err
	}
	for _, q := range c.Requests {
		out.Outcomes[q.Outcome]++
	}
	out.PhysicalAttempts = len(c.Requests)
	if r.s.Custom {
		return out, nil
	}
	if p == nil || c.TargetBuild != p.TargetBuild || c.CorrTestBuild != p.CorrTestBuild || c.WorkloadConfig != p.WorkloadConfig || c.ObserverCPUSeconds == nil || !finite(*c.ObserverCPUSeconds) || *c.ObserverCPUSeconds < 0 {
		return out, errors.New("full-cost provenance or measured overhead unavailable")
	}
	out.ObserverCPUSeconds = *c.ObserverCPUSeconds
	traces := map[string]attemptTrace{}
	seen := map[string]bool{}
	for _, a := range c.Attempts {
		if !digest(a.TraceID, 16) || seen[a.TraceID] || !digest(a.RequestID, 32) || traces[a.RequestID].TraceID != "" {
			return out, errors.New("missing or duplicate request/trace identity")
		}
		traces[a.RequestID] = a
		seen[a.TraceID] = true
	}
	tails := map[string][]float64{}
	for _, q := range c.Requests {
		cohorts := contacts[q.RequestID]
		if len(cohorts) == 0 {
			continue
		} // resolved-only group, outside cost cohort
		a, ok := traces[q.RequestID]
		if !ok || a.Started.Before(c.From) || a.Started.After(q.Started) || a.Finished.Before(q.Finished) || a.Finished.After(c.Through) {
			return out, errors.New("request-side attempt lacks its complete attributable trace")
		}
		delete(traces, q.RequestID)
		counts := map[string]int{}
		times := map[string]float64{}
		sql, http := 0, 0
		for _, s := range a.Spans {
			if s.DurationNS < 0 || s.OffsetNS < 0 || s.OffsetNS > a.Finished.Sub(a.Started).Nanoseconds() || s.DurationNS > a.Finished.Sub(a.Started).Nanoseconds()-s.OffsetNS {
				return out, errors.New("invalid monotonic span interval")
			}
			switch s.Category {
			case "journal_http", "other_http":
				http++
			case "policy_select", "journal_sql", "history_settlement_lock", "canonical_enrollment", "tracked_lifecycle", "tracked_coverage", "other_sql":
				sql++
			case "scope_never_enrolled", "native_direct", "canonical_entry", "settlement_ineligible":
			default:
				return out, errors.New("unclassified trace event")
			}
			counts[s.Category]++
			times[s.Category] += time.Duration(s.DurationNS).Seconds()
		}
		if a.SQLSeen == nil || a.HTTPSeen == nil || a.Dropped == nil || *a.Dropped != 0 || sql != *a.SQLSeen || http != *a.HTTPSeen {
			return out, errors.New("trace dropped spans or differs from original per-transport event counts")
		}
		for cohort := range cohorts {
			if out.Counts[cohort] == nil {
				out.Counts[cohort] = map[string]int{}
				out.Seconds[cohort] = map[string]float64{}
			}
			out.Attempts[cohort]++
			for k, n := range counts {
				out.Counts[cohort][k] += n
				out.Seconds[cohort][k] += times[k]
			}
			if counts["scope_never_enrolled"] > 0 && counts["policy_select"]+counts["journal_http"]+counts["journal_sql"] > 0 {
				t.fail("never-enrolled target acquired journal or policy traversal")
			}
			if counts["policy_select"] > 0 {
				t.fail("ordinary delivery selected policies on the delivery path")
			}
			if cohort == "direct" {
				if counts["scope_never_enrolled"] != 1 && p.Role != domain.CampaignPreChangeControl || counts["native_direct"] != 1 {
					t.miss("direct recipient scope/entry is unverified")
				}
				if counts["journal_http"]+counts["journal_sql"] > 0 {
					t.fail("never-enrolled direct attempt traversed the native journal")
				}
				if p.Role != domain.CampaignPreChangeControl && counts["history_settlement_lock"] == 0 && counts["settlement_ineligible"] == 0 {
					t.miss("shared settlement eligibility/locking read not measured")
				}
			} else if counts["canonical_entry"] == 0 {
				t.miss("unverified canonical trace")
			}
			tails[cohort] = append(tails[cohort], a.Finished.Sub(a.Started).Seconds())
		}
	}
	if p.Role != domain.CampaignPreChangeControl {
		if out.Counts["canonical_first"]["history_settlement_lock"]+out.Counts["canonical_repeat"]["history_settlement_lock"] == 0 {
			t.miss("no measured eligible native physical settlement lock across canonical targets")
		}
		if out.Counts["canonical_first"]["canonical_enrollment"] == 0 {
			t.miss("first canonical enrollment cost missing")
		}
		if out.Counts["canonical_repeat"]["tracked_lifecycle"] == 0 || out.Counts["canonical_repeat"]["tracked_coverage"] == 0 {
			t.miss("recurring canonical lifecycle/coverage cost missing")
		}
	}
	if len(traces) != 0 {
		return out, errors.New("unmatched trace outside observed firing request population")
	}
	for _, cohort := range []string{"direct", "canonical_first", "canonical_repeat"} {
		if len(tails[cohort]) == 0 {
			return out, fmt.Errorf("no observed %s attempts", cohort)
		}
		out.Headlines = append(out.Headlines, percentiles("native."+cohort, tails[cohort])...)
	}
	return out, nil
}
func digest(s string, n int) bool { b, err := hex.DecodeString(s); return err == nil && len(b) == n }
