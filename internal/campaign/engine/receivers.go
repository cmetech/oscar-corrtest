package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/receivers"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// ReceiverOps is the receivers lane's OSCAR surface (targetops.Receivers).
type ReceiverOps interface {
	Mapping(ctx context.Context, ns, name string) ([]receivers.MappingElement, bool, error)
	MappingNames(ctx context.Context, ns, prefix string) ([]string, error)
	CreateMapping(ctx context.Context, ns, name, description string, els []receivers.MappingElement) error
	DeleteMapping(ctx context.Context, ns, name string) error
	Send(ctx context.Context, receiver, profile string, p receivers.Probe) error
	ProbeHistory(ctx context.Context, start, end time.Time) ([]oscar.HistoryRecord, error)
	Reachable(ctx context.Context) []string
}

// kindRxMapping journals a Mapping the receivers lane installs: ObjectID is
// "<namespace>/<name>", recorded before its create.
const kindRxMapping = "rx_mapping"

// rxProfilePrefix names campaign webhook profiles (profile names are
// ^[a-z0-9][a-z0-9_-]{0,63}$).
const rxProfilePrefix = "ct-rx-"

// rxReadMargin is how long after the last probe's Bound the lane reads back.
const rxReadMargin = 30 * time.Second

type rxMapping struct{ Namespace, Name string }

func (r *run) rxProfile() string { return rxProfilePrefix + maintToken(r.id) }

func (r *run) rxMappings() []struct {
	m   rxMapping
	els []receivers.MappingElement
} {
	return []struct {
		m   rxMapping
		els []receivers.MappingElement
	}{{rxMapping{receivers.ProfileNamespace, r.rxProfile()}, receivers.WebhookProfile()},
		{rxMapping{receivers.TrapNamespace, receivers.TrapOID}, receivers.TrapMapping()}}
}

func (r *run) setRx(f func(*score.ReceiverStats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.rx)
}

// rxTicks are the probe offsets: every Every from From to Until, except
// within 30 s of a spike.
func rxTicks(rx scenario.ReceiversSpec, bursts []scenario.BurstSpec) []time.Duration {
	var out []time.Duration
	for at := rx.From; at <= rx.Until; at += rx.Every {
		spike := false
		for _, b := range bursts {
			if at >= b.At-30*time.Second && at < b.At+b.For+30*time.Second {
				spike = true
			}
		}
		if !spike {
			out = append(out, at)
		}
	}
	return out
}

// rxServer is the server of probe k (0-based over every receiver's probes):
// round robin over the correlation servers, after the worker's.
func (r *run) rxServer(k int) int {
	return r.s.Baseline.Servers + 1 + k%max(r.s.Correlation.Servers, 1)
}

// receiversPhase runs the receivers lane (Scenario.Receivers, OSC-207).
func (r *run) receiversPhase(ctx context.Context) {
	spec := r.s.Receivers
	if spec == nil {
		return
	}
	ticks := rxTicks(*spec, r.s.Baseline.Bursts)
	r.setRx(func(s *score.ReceiverStats) {
		s.Planned, s.Bound = len(ticks)*len(receivers.Names), spec.Bound.Seconds()
	})
	if slices.Contains(r.opts.SkipLanes, receivers.Lane) {
		r.setRx(func(s *score.ReceiverStats) { s.Skipped = laneSkipped })
		return
	}
	ops := r.e.d.Receivers
	if ops == nil {
		r.setRx(func(s *score.ReceiverStats) { s.Setup = "no receivers client" })
		return
	}
	stable := context.WithoutCancel(ctx)
	ids, err := r.rxSetup(ctx, ops)
	if err != nil {
		r.setRx(func(s *score.ReceiverStats) { s.Setup = err.Error() })
		r.setRx(func(s *score.ReceiverStats) { s.Removed = r.rxRemove(stable, ids) })
		return
	}
	profile := r.rxProfile()
	var probes []score.ReceiverProbe
	var lastRead time.Time
	for n, off := range ticks {
		if !r.e.clock.SleepUntil(ctx, r.t0.Add(off)) {
			break
		}
		for i, rx := range receivers.Names {
			p := score.ReceiverProbe{Receiver: rx, Name: fmt.Sprintf("Campaign-%s-rx-%s-%d", r.id, rx, n+1), Server: r.rxServer(n*len(receivers.Names) + i)}
			labels := score.ReceiverLabels(r.id, p.Server)
			labels["alertname"], labels["severity"] = p.Name, "warning"
			p.SentAt = r.now()
			if err := ops.Send(ctx, rx, profile, receivers.Probe{Labels: labels, StartsAt: p.SentAt.UTC().Truncate(time.Millisecond)}); err != nil {
				p.SendErr = err.Error()
			}
			probes = append(probes, p)
		}
		if r.now().Sub(lastRead) >= rxReadEvery {
			lastRead = r.now()
			_ = r.rxRead(stable, ops, probes, spec.Bound, false)
		}
	}
	if len(probes) > 0 {
		r.e.clock.SleepUntil(ctx, probes[len(probes)-1].SentAt.Add(spec.Bound+rxReadMargin)) // a stop reads back at once
		if err := r.rxRead(stable, ops, probes, spec.Bound, true); err != nil {
			r.setRx(func(s *score.ReceiverStats) { s.Read = err.Error() })
		}
	}
	removed := r.rxRemove(stable, ids)
	r.setRx(func(s *score.ReceiverStats) { s.Probes, s.Removed = probes, removed })
}

// rxReadEvery spaces the rolling History reads.
const rxReadEvery = time.Minute

// rxRead looks for the probes not yet seen whose Bound (plus a margin) has
// passed (every one when final) and judges each at its first sighting, so
// its status is read minutes, not hours, after its send (an auto-resolve or
// expiry rule cannot turn it resolved first); seen rows are resolved at once
// (cleanup re-checks every campaign row). A probe not seen stays pending.
func (r *run) rxRead(ctx context.Context, ops ReceiverOps, probes []score.ReceiverProbe, bound time.Duration, final bool) error {
	now := r.now()
	var due []int
	from := now
	for i, p := range probes {
		if !p.Found && p.SendErr == "" && (final || !p.SentAt.Add(bound+rxReadMargin).After(now)) {
			due = append(due, i)
			if p.SentAt.Before(from) {
				from = p.SentAt
			}
		}
	}
	if len(due) == 0 {
		return nil
	}
	rows, err := ops.ProbeHistory(ctx, from.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		return err
	}
	byName := map[string]oscar.HistoryRecord{}
	for _, row := range rows {
		if row.Labels["campaign_run"] == r.id {
			byName[row.AlertName] = row
		}
	}
	for _, i := range due {
		row, ok := byName[probes[i].Name]
		if !ok {
			continue
		}
		probes[i].Found, probes[i].CreatedAt = true, row.CreatedAt
		probes[i].Problem = score.CheckReceiverRow(r.id, probes[i], row)
		if req, err := oscar.BuildHistoryResolve(row, r.id); err == nil {
			_, _ = r.e.d.Target.PostAlertGroup(ctx, req)
		}
	}
	return nil
}

// rxSetup installs the campaign webhook profile and trap mapping, each
// recorded first and read back; a Mapping already present (a leftover B17
// would have refused) is not adopted.
func (r *run) rxSetup(ctx context.Context, ops ReceiverOps) ([]int64, error) {
	var ids []int64
	for _, x := range r.rxMappings() {
		if _, found, err := ops.Mapping(ctx, x.m.Namespace, x.m.Name); err != nil {
			return ids, fmt.Errorf("read %s/%s: %w", x.m.Namespace, x.m.Name, err)
		} else if found {
			return ids, fmt.Errorf("mapping %s/%s already exists", x.m.Namespace, x.m.Name)
		}
		id, err := r.record(ctx, kindRxMapping, x.m.Namespace+"/"+x.m.Name, x.m)
		if err != nil {
			return ids, fmt.Errorf("record %s: %w", kindRxMapping, err)
		}
		ids = append(ids, id)
		if err := ops.CreateMapping(ctx, x.m.Namespace, x.m.Name, "CorrTest receivers lane "+r.id+"; removed by its campaign", x.els); err != nil {
			return ids, fmt.Errorf("create %s/%s: %w", x.m.Namespace, x.m.Name, err)
		}
		els, found, err := ops.Mapping(ctx, x.m.Namespace, x.m.Name)
		if err != nil || !found || !reflect.DeepEqual(els, x.els) {
			return ids, fmt.Errorf("read-back %s/%s: found %v, elements equal %v, err %v", x.m.Namespace, x.m.Name, found, reflect.DeepEqual(els, x.els), err)
		}
	}
	return ids, nil
}

// rxRemove restores the lane's mutations (newest first); true when all are
// (cleanup retries any left open).
func (r *run) rxRemove(ctx context.Context, ids []int64) bool {
	ok := true
	for i := len(ids) - 1; i >= 0; i-- {
		if err := r.restoreNow(ctx, ids[i]); err != nil {
			ok = false
			r.event(ctx, "campaign.receivers", "warn", "remove: "+err.Error(), nil)
		}
	}
	return ok
}

// restoreRx deletes a journaled Mapping and reads back that it is gone.
func (r *run) restoreRx(ctx context.Context, m domain.CampaignMutation) error {
	ops := r.e.d.Receivers
	if ops == nil {
		return fmt.Errorf("no receivers client")
	}
	var x rxMapping
	if err := json.Unmarshal(m.Prior, &x); err != nil || x.Name == "" {
		ns, name, _ := strings.Cut(m.ObjectID, "/")
		x = rxMapping{ns, name}
	}
	if err := ops.DeleteMapping(ctx, x.Namespace, x.Name); err != nil {
		return err
	}
	if _, found, err := ops.Mapping(ctx, x.Namespace, x.Name); err != nil || found {
		return fmt.Errorf("read-back after delete: found %v, err %v", found, err)
	}
	return nil
}

// precheckReceivers is B17's evidence: no campaign Mapping left, the mapping
// API readable, the receivers reachable.
func precheckReceivers(ctx context.Context, ops ReceiverOps) precheck.Result {
	var problems []string
	names, err := ops.MappingNames(ctx, receivers.ProfileNamespace, rxProfilePrefix)
	if err != nil {
		return precheck.ReceiversReady("", nil, err)
	}
	for _, n := range names {
		problems = append(problems, "leftover campaign webhook profile "+n)
	}
	for _, oid := range []string{receivers.TrapOID, receivers.TrapEnterpriseOID} { // an enterprise mapping would run first
		if _, found, err := ops.Mapping(ctx, receivers.TrapNamespace, oid); err != nil {
			return precheck.ReceiversReady("", nil, err)
		} else if found {
			problems = append(problems, "trap mapping "+oid+" already exists (a leftover, or one that would act on the campaign trap)")
		}
	}
	problems = append(problems, ops.Reachable(ctx)...)
	return precheck.ReceiversReady("mapping API readable; webhook route and SMTP answer; trap community set (UDP is not checked without a trap)", problems, nil)
}

// rxScore judges the receivers lane into the scorecard.
func (r *run) rxScore(card *score.Scorecard, report map[string]any) {
	if r.s.Receivers == nil {
		return
	}
	r.mu.Lock()
	s := r.rx
	s.Probes = append([]score.ReceiverProbe(nil), r.rx.Probes...)
	r.mu.Unlock()
	fails, incon := score.JudgeReceivers(&s)
	if s.Skipped != "" {
		card.SkippedLanes = append(card.SkippedLanes, receivers.Lane+": "+s.Skipped)
	}
	card.Receivers = &s
	report["receivers"] = s
	judge(card, fails, incon)
}
