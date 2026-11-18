package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Fingerprint-policy lane (scenario facet Fingerprint). OSCAR has no DELETE
// for fingerprint policies, so every run reuses one fixed policy name; it is
// published enabled for the lane and published disabled again (forward-only)
// by the lane or, failing that, by cleanup. The lane's alerts carry
// campaign_run, so the drain and cleanup resolve them from History by their
// exact OSCAR fingerprint, whatever the policy state by then.

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
	startsAt := r.now().Truncate(time.Second)
	sent := 0
	post := func(device, volatile string) {
		labels := map[string]string{"alertname": fingerprintAlert, "campaign_run": r.id, "campaign_lane": fingerprintLane,
			"device": device, volatileLabel: volatile, "severity": "warning", "create_ticket": "false"}
		if status, err := r.e.d.Target.PostAlert(ctx, Alert{Name: fingerprintAlert, Labels: labels, StartsAt: startsAt}); err == nil && status/100 == 2 {
			sent++
		}
	}
	for i := 1; i <= spec.Repeats; i++ {
		post(mergeDevice, fmt.Sprintf("v%d", i))
	}
	post(splitDevice, "v1")
	r.setFP(func(s *score.FingerprintStats) { s.Sent = sent })
	// ponytail: one read after Verify; poll for early completion if Verify must be long.
	if !r.e.clock.SleepUntil(ctx, startsAt.Add(spec.Verify)) {
		note(false, "campaign ended before the History read-back")
		return
	}
	rows, err := r.e.d.Target.History(ctx, r.id, startsAt.Add(-time.Minute), r.now().Add(time.Minute))
	if err != nil {
		note(false, "History read-back: "+err.Error())
		return
	}
	r.setFP(func(s *score.FingerprintStats) { s.Observe(rows, fingerprintAlert, mergeDevice, splitDevice) })
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

// disableFingerprint is the kindFingerprintPolicy restore.
func (r *run) disableFingerprint(ctx context.Context) error {
	if r.e.d.Fingerprint == nil {
		return errors.New("no fingerprint policy client")
	}
	return r.e.d.Fingerprint.Disable(ctx, fingerprintPolicy, r.fpOp("off"), r.fpConverge())
}
