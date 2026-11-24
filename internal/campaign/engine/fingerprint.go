package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Fingerprint-policy lane (scenario facet Fingerprint). OSCAR has no DELETE
// for fingerprint policies, so every run reuses one fixed policy name; it is
// published enabled for the lane and published disabled again (forward-only)
// by the lane or, failing that, by cleanup. The lane's History rows have an
// identity only the policy produces: once it is reverted, OSCAR computes the
// legacy identity for a resolve, which matches nothing. So the policy restore
// resolves the lane's alerts, and reads them back resolved, before it reverts
// (camp_19782e09e46265eb left a merged row firing for good).

const (
	// kindFingerprintPolicy: ObjectID is the policy name; restore publishes it disabled.
	kindFingerprintPolicy = "fingerprint_policy_enable"
	fingerprintLane       = "fingerprint"
	fingerprintPolicyName = "corrtest-fp-ignore-volatile"
	fingerprintAlert      = "CORRTEST_FINGERPRINT_POLICY"
	volatileLabel         = "corrtest_volatile"
	mergeDevice           = "corrtest-fp-merge"
	splitDevice           = "corrtest-fp-split"
	// defaultFingerprintConverge covers consumers' ~30 s refresh with margin.
	defaultFingerprintConverge = 2 * time.Minute
)

// fingerprintPolicy ignores volatileLabel on fingerprintAlert only.
var fingerprintPolicy = targetops.IgnoreLabelPolicy(fingerprintPolicyName, fingerprintAlert, volatileLabel)

// FingerprintPolicies is the fingerprint-policy surface (targetops.FingerprintPolicies).
type FingerprintPolicies interface {
	// Preflight says why the lane must be skipped, "" when it can run.
	Preflight(ctx context.Context) (string, error)
	// Enable publishes def enabled and returns the publication once every consumer serves it.
	Enable(ctx context.Context, def targetops.FingerprintDefinition, opID string, wait time.Duration) (string, error)
	// Disable reverts def (published disabled and served), with read-back.
	Disable(ctx context.Context, def targetops.FingerprintDefinition, opID string, wait time.Duration) error
}

func (r *run) setFP(f func(*score.FingerprintStats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.fp)
}

// fpOp is a fresh operation id: OSCAR operation ids are idempotent per actor
// and payload, so every activation (and every cleanup re-run) needs its own.
func (r *run) fpOp(phase string) string {
	return fmt.Sprintf("corrtest-%s-%s-%d", r.id, phase, r.now().UnixMilli())
}

func (r *run) fpConverge() time.Duration {
	if f := r.s.Fingerprint; f != nil && f.Converge > 0 {
		return f.Converge
	}
	return defaultFingerprintConverge
}

// fingerprintPhase runs the lane at T0+At: pre-flight (skip when policies are
// not commissioned), enable and wait for every consumer, post Repeats merge
// variants and one split control with one startsAt, read History back after
// Verify, then revert the policy.
func (r *run) fingerprintPhase(ctx context.Context) {
	spec := r.s.Fingerprint
	if spec == nil {
		return
	}
	stable := context.WithoutCancel(ctx)
	note := func(skipped bool, why string) {
		word := "inconclusive"
		if skipped {
			word = "skipped"
		}
		r.setFP(func(s *score.FingerprintStats) {
			if skipped {
				s.Skipped = why
			} else {
				s.Inconclusive = why
			}
		})
		r.event(stable, "campaign.fingerprint", "warn", "fingerprint lane "+word+": "+why, nil)
	}
	fp := r.e.d.Fingerprint
	if fp == nil {
		note(true, "no fingerprint policy client")
		return
	}
	if !r.e.clock.SleepUntil(ctx, r.t0.Add(spec.At)) {
		note(false, "campaign ended before the fingerprint lane")
		return
	}
	why, err := fp.Preflight(ctx)
	if err != nil {
		why = "pre-flight: " + err.Error()
	}
	if why != "" {
		note(true, why)
		return
	}
	mid, err := r.record(stable, kindFingerprintPolicy, fingerprintPolicyName, map[string]string{"name": fingerprintPolicyName})
	if err != nil {
		note(false, "record policy mutation: "+err.Error())
		return
	}
	defer r.revertFingerprint(stable, mid)
	pub, err := fp.Enable(ctx, fingerprintPolicy, r.fpOp("on"), r.fpConverge())
	if err != nil {
		note(errors.Is(err, targetops.ErrFingerprintNotCommissioned), "enable: "+err.Error())
		return
	}
	r.setFP(func(s *score.FingerprintStats) { s.PublicationID = pub })
	r.event(stable, "campaign.fingerprint", "info", "fingerprint policy served in publication "+pub, nil)
	// Every > 0 (v4) repeats the round on its own devices while the policy
	// stays on, as long as a round can be verified before the drain.
	first := r.now()
	for k := 1; r.fingerprintRound(ctx, k, note); k++ {
		next := first.Add(time.Duration(k) * spec.Every)
		if spec.Every <= 0 || !next.Add(spec.Verify).Before(r.at("drain", false)) || !r.e.clock.SleepUntil(ctx, next) {
			return
		}
	}
}

// fpDevices are round k's merge and split devices; a single-round lane keeps
// the fixed names.
func (r *run) fpDevices(k int) (merge, split string) {
	if r.s.Fingerprint == nil || r.s.Fingerprint.Every <= 0 {
		return mergeDevice, splitDevice
	}
	return fmt.Sprintf("%s-%d", mergeDevice, k), fmt.Sprintf("%s-%d", splitDevice, k)
}

// fingerprintRound posts Repeats merge variants and one split control with one
// startsAt, reads History back after Verify and scores the round. In a
// multi-round lane it then resolves the round's rows while the policy is on.
// It reports whether the round was verified.
func (r *run) fingerprintRound(ctx context.Context, k int, note func(bool, string)) bool {
	spec := r.s.Fingerprint
	merge, split := r.fpDevices(k)
	startsAt := r.now().Truncate(time.Second)
	sent := 0
	post := func(device, volatile string) {
		labels := r.fpLabels(device, volatile)
		r.offer(fingerprintLane, false, 1)
		if status, err := r.e.d.Target.PostAlert(ctx, Alert{Name: fingerprintAlert, Labels: labels, StartsAt: startsAt}); err == nil && status/100 == 2 {
			sent++
		}
	}
	for i := 1; i <= spec.Repeats; i++ {
		post(merge, fmt.Sprintf("v%d", i))
	}
	post(split, "v1")
	r.setFP(func(s *score.FingerprintStats) { s.Sent += sent })
	// ponytail: one read after Verify; poll for early completion if Verify must be long.
	if !r.e.clock.SleepUntil(ctx, startsAt.Add(spec.Verify)) {
		note(false, "campaign ended before the History read-back")
		return false
	}
	rows, err := r.e.d.Target.History(ctx, r.id, startsAt.Add(-time.Minute), r.now().Add(time.Minute))
	if err != nil {
		note(false, "History read-back: "+err.Error())
		return false
	}
	r.setFP(func(s *score.FingerprintStats) { s.Observe(rows, fingerprintAlert, merge, split) })
	if spec.Every > 0 { // cleanup resolves (and verifies) anything this leaves firing
		for _, row := range rows {
			if d := row.Labels["device"]; row.AlertName == fingerprintAlert && (d == merge || d == split) && !strings.EqualFold(row.Status, "resolved") {
				for _, req := range r.pairResolves(row) {
					_, _ = r.e.d.Target.PostAlertGroup(ctx, req)
					r.offer(fingerprintLane, true, 1)
				}
			}
		}
	}
	return true
}

// revertFingerprint publishes the policy disabled now; on failure the
// mutation stays open for cleanup, which reports DIRTY if it cannot either.
func (r *run) revertFingerprint(ctx context.Context, id int64) {
	if err := r.restoreNow(ctx, id); err != nil {
		r.event(ctx, "campaign.fingerprint", "error", "revert fingerprint policy: "+err.Error(), nil)
		return
	}
	r.setFP(func(s *score.FingerprintStats) { s.Reverted = true })
	r.event(ctx, "campaign.fingerprint", "info", "fingerprint policy reverted", nil)
}

// disableFingerprint is the kindFingerprintPolicy restore: resolve the lane's
// alerts while the policy still serves their identity, then revert it. When
// they do not resolve the policy stays on and the mutation open.
func (r *run) disableFingerprint(ctx context.Context) error {
	if r.e.d.Fingerprint == nil {
		return errors.New("no fingerprint policy client")
	}
	if err := r.resolveFingerprintRows(ctx); err != nil {
		return fmt.Errorf("fingerprint policy kept on until its alerts resolve: %w", err)
	}
	return r.e.d.Fingerprint.Disable(ctx, fingerprintPolicy, r.fpOp("off"), r.fpConverge())
}

// fingerprintFiring is the lane's History rows still firing.
func (r *run) fingerprintFiring(ctx context.Context) ([]oscar.HistoryRecord, error) {
	if r.t0.IsZero() { // no traffic was sent
		return nil, nil
	}
	rows, err := r.e.d.Target.History(ctx, r.id, r.t0.Add(-time.Minute), r.now().Add(time.Minute))
	if err != nil {
		return nil, err
	}
	var firing []oscar.HistoryRecord
	for _, row := range rows {
		if row.AlertName == fingerprintAlert && row.Labels["campaign_run"] == r.id && !strings.EqualFold(row.Status, "resolved") {
			firing = append(firing, row)
		}
	}
	return firing, nil
}

// resolveFingerprintRows resolves the lane's firing rows and repeats until
// History reads them back resolved, within historyResolveBudget.
func (r *run) resolveFingerprintRows(ctx context.Context) error {
	deadline := r.now().Add(historyResolveBudget)
	for {
		firing, err := r.fingerprintFiring(ctx)
		if err != nil {
			return fmt.Errorf("History read-back: %w", err)
		}
		if len(firing) == 0 {
			return nil
		}
		if !r.now().Before(deadline) {
			return fmt.Errorf("%d alerts still firing in History after resolve: %s", len(firing), historyNames(firing))
		}
		for _, row := range firing {
			for _, req := range r.pairResolves(row) {
				_, _ = r.e.d.Target.PostAlertGroup(ctx, req)
			}
		}
		if !r.e.clock.SleepUntil(ctx, r.now().Add(heartbeatEvery)) {
			return fmt.Errorf("stopped with %d alerts firing: %s", len(firing), historyNames(firing))
		}
	}
}

// fpLabels is the label set of one lane alert.
func (r *run) fpLabels(device, volatile string) map[string]string {
	return map[string]string{"alertname": fingerprintAlert, "campaign_run": r.id, "campaign_lane": fingerprintLane,
		"device": device, volatileLabel: volatile, "severity": "warning", "create_ticket": "false"}
}

// pairResolves resolves every Alertmanager alert the lane sent into row. Each
// merged repeat stays its own (am_fingerprint, startsAt) pair on the row and
// History's am_fingerprint names only the latest; OSCAR closes the row only
// once every pair resolves (camp_19782e09e46265eb kept 4 of 5 open). The split
// control is the single alert v1.
func (r *run) pairResolves(row oscar.HistoryRecord) []oscar.AlertGroupRequest {
	repeats := 1
	if strings.HasPrefix(row.Labels["device"], mergeDevice) && r.s.Fingerprint != nil {
		repeats = r.s.Fingerprint.Repeats
	}
	var out []oscar.AlertGroupRequest
	for i := 1; i <= repeats; i++ {
		labels := r.fpLabels(row.Labels["device"], fmt.Sprintf("v%d", i))
		sent, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: fingerprintAlert, Status: "firing", Labels: labels})
		if err != nil {
			continue
		}
		pair := row
		pair.Labels = labels
		pair.Labels["am_fingerprint"] = sent.Alerts[0].Fingerprint
		if req, err := oscar.BuildHistoryResolve(pair, r.id); err == nil {
			out = append(out, req)
		}
	}
	return out
}

// reresolveFingerprint recovers rows left firing after the policy was already
// reverted (a revert before the resolve, or a crash between them): it
// re-enables the policy, which restores their identity, and restores it again,
// which resolves them first. Failing that, cleanup is DIRTY saying why.
func (r *run) reresolveFingerprint(ctx context.Context) {
	firing, err := r.fingerprintFiring(ctx)
	if err != nil || len(firing) == 0 || r.e.d.Fingerprint == nil {
		return // verifyHistoryResolved reports a failed read or a missing client
	}
	open, err := r.store().OpenMutations(ctx, r.id)
	if err != nil {
		return
	}
	for _, m := range open {
		if m.Kind == kindFingerprintPolicy {
			return // still on: its failed restore is already DIRTY
		}
	}
	r.event(ctx, "campaign.cleanup", "warn", fmt.Sprintf("%d %s alerts firing with the fingerprint policy reverted; re-enabling it to resolve them", len(firing), fingerprintAlert), nil)
	mid, err := r.record(ctx, kindFingerprintPolicy, fingerprintPolicyName, map[string]string{"name": fingerprintPolicyName})
	if err == nil {
		if _, err = r.e.d.Fingerprint.Enable(ctx, fingerprintPolicy, r.fpOp("reon"), r.fpConverge()); err == nil {
			err = r.restoreNow(ctx, mid)
		}
	}
	if err != nil {
		r.addDirty(fmt.Sprintf("%s alerts fired under the fingerprint policy and resolve only while it is on; re-enable, resolve, revert failed: %v", fingerprintAlert, err))
	}
}
