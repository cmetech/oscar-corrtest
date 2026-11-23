package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/google/uuid"
)

type managedDeps struct {
	setupErr  error
	originals interface {
		Original(context.Context, string, string) (managed.OriginalReceipt, error)
	}
	config managed.Config
	actor  *managed.Actor
	oscar  *oscar.Client
	sink   *targetops.Sink
	snow   *targetops.SNOW
}

func (d *managedDeps) precheck(ctx context.Context) precheck.Result {
	result := precheck.Result{ID: "TL1", Tier: precheck.Blocking, Status: precheck.Fail}
	if d == nil {
		result.Detail = "managed TL1 fixture not configured"
		return result
	}
	if d.setupErr != nil {
		result.Detail = d.setupErr.Error()
		return result
	}
	if err := d.config.Validate(); err != nil {
		result.Detail = err.Error()
		return result
	}
	if err := d.config.CheckAccess(ctx); err != nil {
		result.Detail = err.Error()
		return result
	}
	if err := d.actor.Session(ctx); err != nil {
		result.Detail = err.Error()
		return result
	}
	result.Status = precheck.Pass
	result.Detail = "private fixture files and operator session available; authenticated TL1, managed source and destination proof required during workload"
	return result
}

type managedLane struct {
	definition targetops.PolicyDefinition
	run        *run
	*managedDeps
	peer *managed.Peer

	repeat   *policyState
	policy   *policyState
	tails    sync.WaitGroup
	clearsAt sync.Map
	cleared  sync.Map
	mu       sync.Mutex
	closed   map[string]closedSubject
	open     map[string]string // AID -> condition; only this run's PDUs
}

func (r *run) managedOnly() bool { return r.s.ID == "managed-alerts-smoke" && r.s.Version == "v1" }
func (r *run) prepareManaged(ctx context.Context) error {
	w := r.s.NativeWorkload
	if w == nil || !w.ManagedTL1 {
		return nil
	}
	if r.e.d.managed == nil || r.e.d.managed.setupErr != nil || w.ManagedPolicy == nil || w.ManagedPolicy.Escalation == nil {
		return errors.New("managed source and timed policy required")
	}
	m := &managedLane{run: r, managedDeps: r.e.d.managed, open: map[string]string{}}
	peer, err := managed.Open(m.config)
	if err != nil {
		return err
	}
	m.peer = peer
	login, cancel := context.WithTimeout(ctx, time.Minute)
	err = peer.Await(login)
	cancel()
	if err != nil {
		peer.Close()
		return errors.New("authenticated TL1 login unavailable")
	}
	if err = m.save(ctx, "native.tl1.connected", map[string]string{"tid": m.config.TID, "source": m.config.SourceID}); err == nil {
		err = m.activate(ctx, *w.ManagedPolicy)
	}
	if err != nil {
		peer.Close()
		return err
	}
	if w.ManagedRepeat != nil {
		author := &managedLane{run: r, managedDeps: r.e.d.managed}
		if err = author.activate(ctx, *w.ManagedRepeat); err != nil {
			peer.Close()
			return err
		}
		m.repeat = author.policy
	}
	r.managed = m
	return nil
}
func (r *run) managedPhase(ctx context.Context) {
	w := r.s.NativeWorkload
	if w == nil || !w.ManagedTL1 {
		return
	}
	m := r.managed
	if m == nil {
		r.noteMissing("managed source unavailable")
		return
	}
	ctx, cancel := context.WithDeadline(ctx, r.t0.Add(r.s.LoadEnd))
	defer cancel()
	var err error
	// Preserve intent across controller loss. A missing terminal event requires
	// operator reconciliation on the original collector. Never reconnect to clear.
	if err = m.save(ctx, "native.tl1.started", map[string]string{"source": m.config.SourceID, "tid": m.config.TID}); err != nil {
		r.noteMissing(err.Error())
		return
	}
	var mixed sync.WaitGroup
	if w.ManagedSegment > 0 && m.repeat != nil {
		mixed.Add(1)
		go func() {
			defer mixed.Done()
			for at := r.t0; at.Before(r.t0.Add(r.s.LoadEnd)); at = at.Add(5 * time.Minute) {
				if !r.e.clock.SleepUntil(ctx, at) {
					return
				}
				start := time.Now()
				_, err := r.e.d.Target.PolicyStatus(ctx, m.policy.notifierID, m.policy.spec.Action, m.policy.id)
				r.mu.Lock()
				r.fo["managed_policy_status"] = append(r.fo["managed_policy_status"], score.FOSample{Seconds: time.Since(start).Seconds(), Err: err != nil})
				r.mu.Unlock()
			}
		}()
		for _, p := range []*policyState{m.repeat, m.policy} {
			mixed.Add(1)
			go func() { defer mixed.Done(); m.sustained(ctx, p, w.ManagedSegment) }()
		}
	}
	if w.ManagedRate > 0 {
		mixed.Add(1)
		go func() {
			defer mixed.Done()
			if err := m.mixed(ctx, w.ManagedRate); err != nil {
				r.noteMissing(err.Error())
			}
		}()
	}
	if err = m.exercise(ctx); err != nil {
		r.noteMissing("managed lifecycle: " + err.Error())
	}
	mixed.Wait()
	// Both final sentinels stay active until T+loadEnd even when the final
	// rounded rate offer and pre-boundary samples finish several seconds early.
	if w.ManagedSegment > 0 {
		r.e.clock.SleepUntil(ctx, r.t0.Add(r.s.LoadEnd))
	}

	defer m.tails.Wait()
	cleanCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer stop()
	if err = m.clearOwned(cleanCtx); err != nil {
		r.addDirty(err.Error())
		return
	}

	if err = m.save(cleanCtx, "native.tl1.closed", map[string]bool{"verified": true}); err != nil {
		r.addDirty(err.Error())
	}
}
func (m *managedLane) save(ctx context.Context, kind string, value any) error {
	b, err := json.Marshal(managedEventEvidence(value))
	if err != nil {
		return err
	}
	return m.run.store().AppendCampaignEvent(ctx, m.run.id, kind, "info", kind, b, m.run.now())
}
func (m *managedLane) policyDefinition(p scenario.PolicySpec) (targetops.PolicyDefinition, error) {
	r := m.run
	// The test rule matches only this campaign's wire conditions on the exact
	// commissioned source and TID. Mixed clean/no-policy conditions are distinct.
	scope := p.Key
	if scope == "TL1" {
		scope = "POL"
	}
	condition := fmt.Sprintf(`source_id == %q and node == %q and condition == %q`, m.config.SourceID, m.config.TID, m.condition(scope))
	priority := r.s.NativeWorkload.ManagedPriorities[p.Key]
	if priority <= len(r.s.Resend) {
		return targetops.PolicyDefinition{}, errors.New("invalid managed policy priority")
	}
	name := "Campaign-" + r.id + "-" + p.Key
	nid := r.sinkID
	if nid == "" || p.Interval < time.Second || p.Escalation != nil && p.Escalation.After < time.Second {
		return targetops.PolicyDefinition{}, errors.New("missing controlled recipient or interval")
	}
	sec := int(p.Interval / time.Second)
	def := targetops.PolicyDefinition{NotifierID: nid, Name: name, Action: p.Action, Mode: p.Mode, IntervalSeconds: &sec, Priority: priority, Condition: condition}
	if p.Escalation != nil {
		def.Escalation = &targetops.Escalation{AfterSeconds: int(p.Escalation.After / time.Second), TargetSeverity: p.Escalation.TargetSeverity}
	}
	return def, nil
}
func (m *managedLane) activate(ctx context.Context, p scenario.PolicySpec) error {
	r := m.run
	def, err := m.policyDefinition(p)
	if err != nil {
		return err
	}
	name, nid := def.Name, def.NotifierID
	prior := policyPrior{"", name, nid, p.Action}
	intent, err := r.record(ctx, kindPolicyCreate, name, prior)
	if err != nil {
		return err
	}
	var pol targetops.Policy
	err = r.withRevision(ctx, nid, p.Action, func(rev int) (int, error) {
		var err error
		pol, err = r.e.d.Target.CreateDraft(ctx, def, rev)
		return pol.Revision, err
	})
	if err != nil {
		return err
	}
	prior.ID = pol.ID
	stable := context.WithoutCancel(ctx)
	if _, err = r.record(stable, kindPolicyCreate, pol.ID, prior); err != nil {
		return err
	}
	if err = r.restored(stable, intent); err != nil {
		return err
	}
	if _, err = r.record(stable, kindPolicyActivate, pol.ID, prior); err != nil {
		return err
	}
	err = r.withRevision(ctx, nid, p.Action, func(rev int) (int, error) {
		var err error
		pol, err = r.e.d.Target.Activate(ctx, prior.ID, nid, p.Action, rev, activationWait)
		return pol.Revision, err
	})
	if err != nil {
		return err
	}
	m.definition = def
	m.policy = &policyState{id: prior.ID, name: name, rev: pol.Revision, activeAt: r.now(), active: true, spec: p, notifierID: nid}
	return m.save(ctx, "native.managed.policy", map[string]any{"id": prior.ID, "revision": pol.Revision, "recipient": nid, "definition": def})
}
func (m *managedLane) condition(prefix string) string { return managed.AID(m.run.id, prefix, 0) }
func (m *managedLane) emit(ctx context.Context, aid, cond, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.save(ctx, "native.tl1.intent", map[string]string{"aid": aid, "condition": cond, "code": code}); err != nil {
		return err
	}
	m.mu.Lock()
	m.open[aid] = cond
	m.mu.Unlock()
	if code == "CL" {
		m.cleared.Store(aid, true)
	} else {
		m.cleared.Delete(aid)
	}
	e, err := m.peer.Emit(aid, cond, code)
	if code == "CL" && err == nil {
		m.clearsAt.Store(aid, e.At)
	}
	if logErr := m.save(context.WithoutCancel(ctx), "native.tl1.wire", e); logErr != nil {
		return logErr
	}
	return err
}
func poll(ctx context.Context, fn func() (bool, error)) error {
	for {
		done, err := fn()
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
func (m *managedLane) rows(ctx context.Context, aid string) ([]oscar.HistoryRecord, error) {
	rows, err := m.oscar.HistoryByLabel(ctx, "aid", aid, m.run.t0.Add(-time.Second), m.run.now().Add(time.Second))
	var out []oscar.HistoryRecord
	for _, r := range rows {
		if r.Labels["aid"] == aid && r.Labels["source_id"] == m.config.SourceID && r.Labels["node"] == m.config.TID && r.Labels["integration"] == "tl1_probe" && r.Labels["alert_type"] == "problem" {
			out = append(out, r)
		}
	}
	return out, err
}

type NativeEpisodeFixture struct {
	Source   managed.SourceReceipt
	History  observedHistory
	Bindings []managed.Binding
}

func (m *managedLane) observe(ctx context.Context, aid, sev, previous string) (NativeEpisodeFixture, error) {
	var fixture NativeEpisodeFixture
	budget := 65 * time.Second
	if aid == managed.AID(m.run.id, "AUTO", 0) {
		budget = setBTicketObservation
	}
	wait, stop := context.WithTimeout(ctx, budget)
	defer stop()
	err := poll(wait, func() (bool, error) {
		rows, err := m.rows(wait, aid)
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			source := row.SourceSeverity
			if source == "" && row.Severity != nil {
				source = *row.Severity
			}
			if row.Status == "resolved" || source != sev || row.Labels["severity"] != "" && row.Labels["severity"] != sev {
				continue
			}
			snapshot, err := m.config.Snapshot(wait, row.ID)
			if err != nil {
				return false, err
			}
			for _, f := range snapshot.Source {
				if f.SourceSeverity != sev || f.Episode == previous || f.State != "firing" || f.WireEpisode != row.Annotations["episode_id"] || f.EventID != row.Annotations["event_id"] {
					continue
				}
				if f.ManagedKey == "" || f.Generation < 1 || f.Variant < 1 || f.Fingerprint == "" || f.Authority == "" || f.SourceID != m.config.SourceID || f.TID != m.config.TID || f.Handler != "tl1_probe" || f.Locator != m.config.Locator || f.MappingRevision != m.config.MappingRevision || row.Annotations["alarm_key"] == "" || f.Correlation != row.Labels["correlation_key"] {
					return false, errors.New("source receipt disagrees with commissioned binding or History")
				}
				if f.WireStart == "" || f.WireStart != row.Annotations["episode_started_at"] {
					a, er := time.Parse(time.RFC3339Nano, f.WireStart)
					b, er2 := time.Parse(time.RFC3339Nano, row.Annotations["episode_started_at"])
					if er != nil || er2 != nil || !a.Equal(b) {
						return false, errors.New("source start mismatch")
					}
				}
				if f.RefreshDeadline == "" {
					return false, nil
				}
				reader, ok := m.run.e.d.nativeSender.(nativeReader)
				if !ok {
					return false, errors.New("live native reader unavailable")
				}
				members, err := reader.Members(wait, map[string]string{"aid": aid, "source_id": m.config.SourceID, "node": m.config.TID, "severity": sev})
				if err != nil {
					return false, err
				}
				valid := false
				for _, v := range members {
					if observedMember(v, f, sev, m.run.now()) && len(v.Receivers) == 1 && v.Receivers[0].Name != "" {
						f.Receiver = v.Receivers[0].Name
						valid = true
						f.RefreshDeadline = v.EndsAt.Format(time.RFC3339Nano)
					}
				}
				if !valid {
					return false, nil
				}
				fixture = NativeEpisodeFixture{f, historyEvidence(row), snapshot.Bindings}
				return true, m.save(wait, "native.source_observed", fixture)
			}
		}
		return false, nil
	})
	return fixture, err
}
func (m *managedLane) command(ctx context.Context, kind string, f NativeEpisodeFixture) (managed.CommandReceipt, error) {
	id := f.History.ID
	request := uuid.NewString()
	if err := m.save(ctx, "native.action.intent", map[string]string{"history": id, "kind": kind, "request": request}); err != nil {
		return managed.CommandReceipt{}, err
	}
	owner, err := m.run.record(ctx, kindManagedOwner, request, originalOwner{Kind: kind, History: id, Request: request, AID: f.History.Labels["aid"], Episode: f.History.EpisodeNumber})
	if err != nil {
		return managed.CommandReceipt{}, err
	}
	receipt, err := m.actor.Command(ctx, kind, id, request)
	receipt.Owner, receipt.IssuedRequest = owner, request
	if er := m.save(context.WithoutCancel(ctx), "native.action.result", map[string]any{"history": id, "kind": kind, "request": request, "receipt": receipt}); er != nil {
		return receipt, er
	}
	if err != nil {
		return receipt, err
	}
	if receipt.StatusCode/100 != 2 {
		return receipt, fmt.Errorf("%s refused (%d, %s), original request %s", kind, receipt.StatusCode, receipt.Code, request)
	}
	return receipt, nil
}
func (m *managedLane) clear(ctx context.Context, aid, cond string) error {
	if err := m.emit(ctx, aid, cond, "CL"); err != nil {
		return err
	}
	return m.verifyClear(ctx, aid)
}
func (m *managedLane) verifyClear(ctx context.Context, aid string) error {
	wait, stop := context.WithTimeout(ctx, 55*time.Second)
	defer stop()
	return poll(wait, func() (bool, error) {
		rows, err := m.rows(wait, aid)
		if err != nil {
			return false, err
		}
		if len(rows) == 0 {
			return false, nil
		}
		for _, row := range rows {
			if row.Status != "resolved" {
				return false, nil
			}
		}
		if err = m.save(wait, "native.resolved", map[string]any{"aid": aid, "rows": rows}); err != nil {
			return false, err
		}
		m.mu.Lock()
		delete(m.open, aid)
		m.mu.Unlock()
		return true, nil
	})
}
func (m *managedLane) clearOwned(ctx context.Context) error {
	m.mu.Lock()
	remaining := make(map[string]string, len(m.open))
	for k, v := range m.open {
		remaining[k] = v
	}
	m.mu.Unlock()
	for aid, cond := range remaining {
		// Do not resend a clear, or follow an ambiguous write across a session gap.
		// The original collector retains any pending bytes. Mixed clears are verified
		// here after the last sends, before the listening device releases its socket.
		if _, ok := m.cleared.Load(aid); !ok {
			if err := m.emit(ctx, aid, cond, "CL"); err != nil {
				return fmt.Errorf("owned TL1 cleanup %s: %w", aid, err)
			}
		}
	}
	byCond := map[string]map[string]bool{}
	for aid, cond := range remaining {
		if byCond[cond] == nil {
			byCond[cond] = map[string]bool{}
		}
		byCond[cond][aid] = true
	}
	for cond, aids := range byCond {
		err := poll(ctx, func() (bool, error) {
			rows, err := m.oscar.HistoryByLabel(ctx, "condition", cond, m.run.t0.Add(-time.Second), m.run.now().Add(time.Second))
			if err != nil {
				return false, err
			}
			resolved := map[string]bool{}
			for _, row := range rows {
				aid := row.Labels["aid"]
				if !aids[aid] || row.Labels["source_id"] != m.config.SourceID || row.Labels["node"] != m.config.TID || row.Labels["alert_type"] != "problem" {
					continue
				}
				if row.Status != "resolved" {
					return false, nil
				}
				resolved[aid] = true
			}
			return len(resolved) == len(aids), nil
		})
		if err != nil {
			return fmt.Errorf("owned TL1 clear unverified for %s (%d AIDs): %w", cond, len(aids), err)
		}
		if err = m.save(ctx, "native.tl1.cleared", map[string]any{"condition": cond, "count": len(aids)}); err != nil {
			return err
		}
	}
	return nil
}
func (m *managedLane) mixed(ctx context.Context, rate float64) error {
	start := m.run.t0
	span := m.run.s.LoadEnd
	capacity := make(chan struct{}, 8)
	for i := 0; ; i++ {
		offer, ok := managed.MixedOffer(i, rate, span)
		if !ok {
			if i == 0 {
				return errors.New("managed load has no complete cycles")
			}
			return nil
		}
		if !m.run.e.clock.SleepUntil(ctx, start.Add(offer.At)) {
			return ctx.Err()
		}
		aid := managed.AID(m.run.id, "mixed", offer.Slot)
		scope := "POL"
		if offer.Slot%2 == 0 {
			scope = "PLAIN"
		}
		if err := m.emit(ctx, aid, m.condition(scope), offer.Code); err != nil {
			return err
		}
		if offer.Code == "CL" {
			m.observeRateSlot(ctx, offer.Slot, capacity)
		}
	}
}
func (m *managedLane) ticket(ctx context.Context, f NativeEpisodeFixture, command managed.CommandReceipt) error {
	aid := f.History.Labels["aid"]
	wait, stop := context.WithTimeout(ctx, setBTicketObservation)
	defer stop()
	return poll(wait, func() (bool, error) {
		rows, err := m.rows(wait, aid)
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			if row.ID != f.History.ID || row.EpisodeNumber != f.History.EpisodeNumber || row.Status == "resolved" || row.TicketID == "" {
				continue
			}
			var proof managed.TicketProof
			if command.Operation != "" {
				fact, er := m.config.ActorFact(wait, row.ID, command.IssuedRequest, "manual_ticket")
				if errors.Is(er, managed.ErrActorPending) {
					return false, nil
				}
				if er != nil {
					return false, er
				}
				if fact.Operation != command.Operation || fact.Episode != f.History.EpisodeNumber || fact.Incident != row.TicketID || command.TicketID != "" && fact.Incident != command.TicketID {
					return false, errors.New("History differs from original admitted ticket")
				}
			} else {
				snap, er := m.config.Snapshot(wait, row.ID)
				if er != nil {
					return false, er
				}
				proof, er = automaticTicket(f, snap.Tickets, row.TicketID)
				if er != nil {
					return false, er
				}
				if proof.Flow == "" {
					return false, nil
				}
			}
			inc, err := m.snow.Incident(wait, row.TicketID)
			if err != nil {
				return false, err
			}
			if inc.Number != row.TicketID || inc.SysID == "" || !strings.Contains(inc.ShortDescription, aid) {
				return false, errors.New("test incident ownership not proved")
			}
			// Persist exact provider identity before asserting the handler projection.
			if _, err = m.run.record(wait, kindIncident, inc.Number, map[string]string{"state": inc.State}); err != nil {
				return false, err
			}
			if err = m.save(wait, "native.ticket", map[string]any{"aid": aid, "request": command.IssuedRequest, "command": command.Operation, "commandTicket": command.TicketID, "automatic": proof, "episode": row.EpisodeNumber, "history": row.ID, "incident": inc.Number, "sys_id": inc.SysID, "priority": inc.Priority}); err != nil {
				return false, err
			}
			if inc.Priority != m.config.TicketPriority {
				return false, errors.New("provider priority differs from commissioned handler mapping")
			}
			return true, nil
		}
		return false, nil
	})
}

func controlledNotifiers(ctx context.Context, t Target, desired string) (string, error) {
	all, err := t.Notifiers(ctx)
	if err != nil {
		return "", err
	}
	sink := ""
	for _, n := range all {
		if n.Status == "disabled" {
			continue
		}
		if strings.EqualFold(n.Name, desired) {
			if sink != "" {
				return "", errors.New("ambiguous controlled sink")
			}
			sink = n.ID
			continue
		}
		if !containsFold(keepNotifiers, n.Name) {
			return "", errors.New("external notifier enabled in managed smoke")
		}
	}
	if sink == "" {
		return "", errors.New("controlled sink must already be enabled for managed smoke")
	}
	return sink, nil
}
