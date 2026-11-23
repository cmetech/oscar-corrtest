package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/google/uuid"
)

type suppressionClient interface {
	nativeReader
	Silence(context.Context, lane.SuppressionSubject) (string, error)
	ExpireSilence(context.Context, lane.SuppressionSubject) error
	Inhibitor(context.Context, lane.SuppressionSubject, bool) error
}

const kindSilence = "native_owned_silence"
const kindInhibitor = "native_owned_inhibitor"

func (r *run) inhibitorPost(ctx context.Context, c suppressionClient, s lane.SuppressionSubject, clear bool) error {
	at := r.now()
	err := c.Inhibitor(ctx, s, clear)
	status := 200
	if err != nil {
		status = 0
	}
	r.sourceOffer("native_inhibitor", at, status)
	return err
}

func (r *run) restoreSuppression(ctx context.Context, mut domain.CampaignMutation) error {
	c, ok := r.e.d.nativeSender.(suppressionClient)
	if !ok {
		return errors.New("native suppression transport unavailable")
	}
	var s lane.SuppressionSubject
	if err := json.Unmarshal(mut.Prior, &s); err != nil {
		return err
	}
	if mut.Kind == kindSilence {
		return c.ExpireSilence(ctx, s)
	}
	scope := map[string]string{"alertname": "CorrTestNativeInhibitor", "campaign_control": s.Owner, "aid": s.AID, "source_id": s.Source, "node": s.TID}
	read := func() (bool, error) {
		members, err := c.Members(ctx, scope)
		if err != nil {
			return false, err
		}
		if len(members) == 0 {
			return false, nil
		}
		if len(members) != 1 || !members[0].StartsAt.Equal(s.Start) || s.ControlID != "" && members[0].Fingerprint != s.ControlID {
			return false, errors.New("inhibitor control start ambiguous")
		}
		return members[0].EndsAt.After(r.now()), nil
	}
	active, err := read()
	if err != nil {
		return err
	}
	if !active {
		if r.now().Before(s.End) && s.ControlID == "" {
			return errors.New("original inhibitor acceptance unverified before expiry")
		}
		return nil
	}
	if err = r.inhibitorPost(ctx, c, s, true); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return poll(wait, func() (bool, error) { active, err := read(); return !active, err })
}
func (m *managedLane) startSuppression(ctx context.Context, kind string, s lane.SuppressionSubject, c suppressionClient) (string, int64, error) {
	mutation := kindSilence
	if kind == "inhibit" {
		mutation = kindInhibitor
	}
	intent, err := m.run.record(ctx, mutation, s.Owner, s)
	if err != nil {
		return "", 0, err
	}
	if kind == "silence" {
		s.ControlID, err = c.Silence(ctx, s)
		if err != nil {
			return "", 0, err
		}
	} else {
		if err = m.run.inhibitorPost(ctx, c, s, false); err != nil {
			return "", 0, err
		}
		members, err := c.Members(ctx, map[string]string{"campaign_control": s.Owner, "aid": s.AID, "alertname": "CorrTestNativeInhibitor"})
		if err != nil || len(members) != 1 || !members[0].StartsAt.Equal(s.Start) || members[0].Fingerprint == "" {
			return "", 0, errors.New("inhibitor original native start unverified")
		}
		s.ControlID = members[0].Fingerprint
	}
	id, err := m.run.record(context.WithoutCancel(ctx), mutation, s.ControlID, s)
	if err != nil {
		return "", 0, err
	}
	return s.ControlID, id, m.run.restored(context.WithoutCancel(ctx), intent)
}
func (m *managedLane) suppression(ctx context.Context) error {
	c, ok := m.run.e.d.nativeSender.(suppressionClient)
	if !ok {
		return errors.New("native suppression transport unavailable")
	}
	for _, kind := range []string{"silence", "inhibit"} {
		aid := managed.AID(m.run.id, kind, 0)
		cond := m.condition("POL")
		if err := m.emit(ctx, aid, cond, "MN"); err != nil {
			return err
		}
		minor, err := m.observe(ctx, aid, "minor", "")
		if err != nil {
			return err
		}
		if err = m.emit(ctx, aid, cond, "MJ"); err != nil {
			return err
		}
		f, err := m.observe(ctx, aid, "major", "")
		if err != nil {
			return err
		}
		if minor.Source.Authority != f.Source.Authority || minor.Source.Episode != f.Source.Episode || minor.Source.Generation != f.Source.Generation || minor.Source.Variant >= f.Source.Variant {
			return errors.New("suppression variant continuity unproved")
		}
		pre, err := m.actor.Eligibility(ctx, f.History.ID)
		if err != nil || pre.Episode != f.History.EpisodeNumber || !pre.Actions["resend"].Allowed {
			return errors.New("current Major must be actionable before suppression")
		}
		before, err := m.config.Snapshot(ctx, f.History.ID)
		if err != nil {
			return err
		}
		binding, err := ownedBinding(m.policy, before.Bindings)
		if err != nil {
			return err
		}
		now := m.run.now().UTC().Truncate(time.Millisecond)
		subject := lane.SuppressionSubject{AID: aid, Source: m.config.SourceID, TID: m.config.TID, Owner: "CorrTest-" + m.run.id + "-" + uuid.NewString(), Start: now, End: now.Add(8 * time.Minute)}
		control, mutation, err := m.startSuppression(ctx, kind, subject, c)
		if err != nil {
			return err
		}
		scope := map[string]string{"aid": aid, "node": m.config.TID, "source_id": m.config.SourceID}
		var decision managed.Eligibility
		var subjectView lane.NativeMember
		samples := 0
		suppressed := func() (bool, error) {
			members, err := c.Members(ctx, scope)
			if err != nil {
				return false, err
			}
			for _, v := range members {
				if !observedMember(v, f.Source, "major", m.run.now()) {
					continue
				}
				if kind == "silence" && !slices.Contains(v.Status.SilencedBy, control) || kind == "inhibit" && !slices.Contains(v.Status.InhibitedBy, control) {
					continue
				}
				elig, err := m.actor.Eligibility(ctx, f.History.ID)
				if err != nil {
					return false, err
				}
				d := elig.Actions["resend"]
				if elig.Episode != f.History.EpisodeNumber || d.Allowed || d.Reason != "NATIVE_SUPPRESSED" || !elig.Actions["acknowledge"].Allowed || !elig.Actions["ticket_create"].Allowed {
					return false, nil
				}
				decision = elig
				subjectView = memberEvidence(v)
				samples++
				return true, nil
			}
			return false, nil
		}
		wait, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = poll(wait, suppressed)
		cancel()
		if err != nil {
			return err
		}
		initialDecision := decision
		start := m.run.now()
		active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
		if err != nil {
			return err
		}
		raise := active.Add(m.policy.spec.Escalation.After)
		if !start.Before(raise) {
			return errors.New("suppression started after escalation deadline")
		}
		end := raise.Add(30 * time.Second)
		for m.run.now().Before(end) {
			held, err := suppressed()
			if err != nil || !held {
				return errors.New("original suppression changed in due interval")
			}
			if !m.run.e.clock.SleepUntil(ctx, m.run.now().Add(2*time.Second)) {
				return ctx.Err()
			}
		}
		final, err := m.config.Snapshot(ctx, f.History.ID)
		if err != nil {
			return err
		}
		if err = m.holdUnknown(ctx, f, final); err != nil {
			return err
		}
		pinned, err := ownedBinding(m.policy, final.Bindings)
		if err != nil || pinned != binding {
			return errors.New("suppressed binding changed")
		}
		var dueDispatches []managed.Dispatch
		for _, d := range final.Dispatches {
			due, er := time.Parse(time.RFC3339Nano, d.DueAt)
			if er == nil && d.Binding == binding.ID && d.Revision == binding.Revision && !due.Before(start) && !due.After(final.MeasuredAt) {
				dueDispatches = append(dueDispatches, d)
			}
		}
		if len(dueDispatches) == 0 {
			return errors.New("suppressed interval lacks an original due policy dispatch")
		}
		deliveries, err := m.sink.ManagedReceipts(ctx, aid, start)
		if err != nil {
			return err
		}
		for _, r := range deliveries {
			for _, a := range r.Alerts {
				if a.Status == "firing" && a.Labels["correlation_key"] == f.Source.Correlation {
					return errors.New("suppressed member reached sink")
				}
			}
		}
		for _, e := range final.Effects {
			when, _ := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
			if e.Notifier == m.policy.notifierID && e.Action == "notification" && !when.Before(start) {
				return errors.New("suppressed target confirmed a provider effect")
			}
		}
		if err = m.save(ctx, "native.suppression_changed", map[string]any{"kind": kind, "subject": f, "control": control, "bound": pinned, "firstDecision": initialDecision, "lastDecision": decision, "member": subjectView, "samples": samples, "dispatches": dueDispatches, "start": start, "escalationDue": raise, "end": m.run.now(), "effects": final.Effects, "sink": deliveries}); err != nil {
			return err
		}
		releaseStart := time.Now()
		if err = m.run.restoreNow(ctx, mutation); err != nil {
			return err
		}
		releaseBy := time.Since(releaseStart)
		// Bracket a durable current physical attempt on the controller clock.
		// An attempt already present on the first post-release read has no
		// lower bound proving admission after the owned release.
		attempts := map[string]dispatchWindow{}
		priorRead := time.Time{}
		wait, cancel = context.WithTimeout(ctx, m.policy.spec.Interval+30*time.Second)
		err = poll(wait, func() (bool, error) {
			elig, er := m.actor.Eligibility(wait, f.History.ID)
			if er != nil {
				return false, er
			}
			if elig.Episode != f.History.EpisodeNumber || !elig.Actions["resend"].Allowed {
				return false, nil
			}
			readFrom := time.Now()
			snap, er := m.config.Snapshot(wait, f.History.ID)
			if er != nil {
				return false, er
			}
			for _, d := range snap.Dispatches {
				if d.Attempt == "" || d.Binding != binding.ID || d.Revision != binding.Revision || d.Kind != "escalate" {
					continue
				}
				if _, seen := attempts[d.Attempt]; !seen {
					attempts[d.Attempt] = dispatchWindow{Before: priorRead, By: time.Now()}
				}
			}
			priorRead = readFrom
			if er = m.holdUnknown(wait, f, snap); er != nil {
				return false, er
			}
			rs, er := m.sink.ManagedReceipts(wait, aid, end)
			if er != nil {
				return false, er
			}
			var effects []managed.Effect
			escalated := false
			var resumed dispatchWindow
			for _, e := range snap.Effects {
				at, er := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
				if er != nil || at.Before(end) || e.Notifier != m.policy.notifierID || e.Action != "notification" {
					continue
				}
				if e.BindingID != binding.ID || e.Revision != binding.Revision || e.Receiver != f.Source.Receiver || !routedScope(e, m.policy.notifierID, m.policy.spec.Action) {
					return false, errors.New("released target binding mismatch")
				}
				effects = append(effects, e)
				if e.Kind == "escalate" && e.Severity == "critical" {
					resumed = attempts[e.Operation]
					escalated = true
				}
			}
			if !escalated {
				return false, nil
			}
			ok, er := matchSink(effects, rs, m.policy.notifierID, f.Source.Correlation)
			if er != nil || !ok {
				return false, er
			}
			if resumed.Before.IsZero() || resumed.Before.Sub(releaseStart) < releaseBy || resumed.By.Sub(releaseStart) > 30*time.Second {
				return false, errors.New("resumed durable escalation lacks a qualified release-to-dispatch bracket within 30s")
			}
			return true, m.save(wait, "native.suppression_released", map[string]any{"kind": kind, "control": control, "binding": binding, "escalationDue": raise, "releaseStart": releaseStart.UTC(), "releaseByNs": releaseBy, "dispatchAfterNs": resumed.Before.Sub(releaseStart), "dispatchByNs": resumed.By.Sub(releaseStart), "subject": f, "eligibility": elig, "effects": effects, "receipts": rs})

		})
		cancel()
		if err != nil {
			return err
		}
		if err = m.clear(ctx, aid, cond); err != nil {
			return err
		}
	}
	return nil
}
