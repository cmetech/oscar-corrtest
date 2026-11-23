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
	"sort"
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
	TraceID, OriginalDispatch, Receiver, Notifier string
	ReceiptID                                     int64
	Members                                       []string
	Started, Finished                             time.Time
	SQLSeen, HTTPSeen, Dropped                    *int
	Spans                                         []traceSpan
}
type costFile struct {
	Run, Hash, TargetBuild, CorrTestBuild, WorkloadConfig, ObserverConfig string
	From, Through                                                         time.Time
	SamplingFraction, ObserverCPUSeconds                                  *float64
	Attempts                                                              []attemptTrace
}
type costStats struct {
	CaptureSHA256, ObserverConfig string
	ObserverCPUSeconds            float64
	Attempts                      map[string]int
	Counts                        map[string]map[string]int
	Seconds                       map[string]map[string]float64
	Headlines                     []score.Headline
}

func (r *run) costCapture(t *nativeTally, rs []targetops.Receipt) (costStats, error) {
	var out costStats
	if r.e.d.ObserverDir == "" || r.opts.Provenance == nil {
		return out, errors.New("independent ordinary request/SQL observer not commissioned")
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
	if c.Run != r.id || c.Hash != r.s.Hash() || c.TargetBuild != p.TargetBuild || c.CorrTestBuild != p.CorrTestBuild || c.WorkloadConfig != p.WorkloadConfig || !digest(c.ObserverConfig, 32) || c.From.IsZero() || c.From.After(r.t0) || c.Through.Before(r.t0.Add(r.s.LoadEnd)) || c.SamplingFraction == nil || *c.SamplingFraction != 1 || c.ObserverCPUSeconds == nil || !finite(*c.ObserverCPUSeconds) || *c.ObserverCPUSeconds < 0 {
		return out, errors.New("trace provenance, observer coverage or measured overhead incomplete")
	}
	sum := sha256.Sum256(raw)
	out = costStats{CaptureSHA256: hex.EncodeToString(sum[:]), ObserverConfig: c.ObserverConfig, ObserverCPUSeconds: *c.ObserverCPUSeconds, Attempts: map[string]int{}, Counts: map[string]map[string]int{}, Seconds: map[string]map[string]float64{}}
	byReceipt := map[int64]attemptTrace{}
	seen := map[string]bool{}
	dispatches := map[[2]string]bool{}
	for _, a := range c.Attempts {
		key := [2]string{a.OriginalDispatch, a.Notifier}
		if !digest(a.OriginalDispatch, 32) || dispatches[key] {
			return out, errors.New("missing or duplicate original physical dispatch")
		}
		dispatches[key] = true
		if a.ReceiptID <= 0 || seen[a.TraceID] || !digest(a.TraceID, 16) || byReceipt[a.ReceiptID].ReceiptID != 0 {
			return out, errors.New("duplicate or missing original trace identity")
		}
		seen[a.TraceID] = true
		byReceipt[a.ReceiptID] = a
	}
	seenMember := map[string]bool{}
	tails := map[string][]float64{}
	ordered := append([]targetops.Receipt(nil), rs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ReceivedAt.Before(ordered[j].ReceivedAt) })
	for _, rc := range ordered {
		members := map[string]bool{}
		cohorts := map[string]bool{}
		receiver := ""
		for _, a := range rc.Alerts {
			m, ok := t.ordinary[a.AlertName]
			if !ok || a.Status != "firing" {
				continue
			}
			if len(m.Receivers) != 1 || receiver != "" && receiver != m.Receivers[0].Name {
				return out, errors.New("ambiguous original receiver")
			}
			receiver = m.Receivers[0].Name
			cohort := "direct"
			if m.Labels["campaign_lane"] == "native-canonical" {
				cohort = "canonical_first"
				if seenMember[a.AlertName] {
					cohort = "canonical_repeat"
				}
			}
			cohorts[cohort] = true
			members[a.AlertName] = true
			seenMember[a.AlertName] = true
		}
		if len(members) == 0 {
			continue
		}
		a, ok := byReceipt[rc.ID]
		if !ok {
			return out, errors.New("captured sink contact missing its complete trace")
		}
		delete(byReceipt, rc.ID)
		recipient := rc.Notifier
		if cohorts["direct"] && recipient == "" && rc.Receiver == receiver {
			recipient = rc.Receiver
		}
		if a.Receiver != receiver || a.Notifier == "" || a.Notifier != recipient || len(a.Members) != len(members) || a.Started.Before(c.From) || a.Finished.After(c.Through) || rc.ReceivedAt.Before(a.Started) || rc.ReceivedAt.After(a.Finished) {
			return out, errors.New("trace source/recipient/time differs from sink request")
		}
		for _, id := range a.Members {
			if !members[id] {
				return out, errors.New("trace group differs from original source members")
			}
			delete(members, id)
		}
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
	if len(byReceipt) != 0 {
		return out, errors.New("observer recorded extra physical contacts outside captured member set")
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
