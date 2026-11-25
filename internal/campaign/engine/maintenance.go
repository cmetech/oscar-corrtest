package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// MaintOps is OSCAR's maintenance-mode API (targetops.UserAPI, as fo-01).
type MaintOps interface {
	MaintPatterns(ctx context.Context) ([]targetops.MaintPattern, error)
	MaintTemplates(ctx context.Context) ([]targetops.MaintTemplate, error)
	CreateMaintPattern(ctx context.Context, p targetops.MaintPattern) error
	CreateMaintTemplate(ctx context.Context, t targetops.MaintTemplate) error
	DeleteMaintPattern(ctx context.Context, key string) error
	DeleteMaintTemplate(ctx context.Context, key string) error
	EnableMaint(ctx context.Context, w targetops.MaintWindow) (targetops.MaintEnabled, error)
	DisableMaint(ctx context.Context, cr string) error
	MaintStatus(ctx context.Context, cr string) (targetops.MaintStatus, error)
	MaintRecords(ctx context.Context, q string) ([]targetops.MaintRecord, error)
	MaintLive(ctx context.Context, id string) (string, error)
}

// Maintenance mutation kinds: ObjectID is the pattern or template key, or the
// window's CR; each is recorded before its create.
const (
	kindMaintPattern  = "maint_pattern"
	kindMaintTemplate = "maint_template"
	kindMaintWindow   = "maint_window"
)

const (
	maintLane    = "maintenance"
	maintPrefix  = "ct-maint-"
	maintLabel   = "campaign_maint"
	maintHandler = "alerthistory"
	// maintEdge is how long after a window edge its status is read; a read
	// more than maintEdge late is not judged (B4 bounds the clock skew).
	maintEdge = 15 * time.Second
	// maintPoll and maintReadBudget bound the History read-back.
	maintPoll       = 10 * time.Second
	maintReadBudget = 2 * time.Minute
)

// maintToken is the run's token in maintenance keys: OSCAR keys are
// lowercase slugs (^[a-z0-9][a-z0-9-]{0,62}$), so not the camp_ run id; any
// other character becomes '-', and the token leaves room for the prefix and
// the "-ctl" control value.
func maintToken(id string) string {
	t := []byte(strings.ToLower(strings.TrimPrefix(id, "camp_")))
	for i, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			t[i] = '-'
		}
	}
	return string(t[:min(len(t), 63-len(maintPrefix)-len("-ctl"))])
}

// maintKey is the campaign pattern key, template key and window CR.
func (r *run) maintKey() string { return maintPrefix + maintToken(r.id) }

// maintBounds is the window, in whole seconds: OSCAR stores its bounds as
// MySQL DATETIME, so sub-second bounds would read back rounded.
func (r *run) maintBounds() (start, end time.Time) {
	m := r.s.Maintenance
	start = r.t0.Add(m.Start).UTC().Truncate(time.Second)
	return start, start.Add(m.For)
}

func (r *run) setMaint(f func(*score.MaintenanceStats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.maint)
}

// maintenancePhase runs the maintenance-mode lane (Scenario.Maintenance).
func (r *run) maintenancePhase(ctx context.Context) {
	spec := r.s.Maintenance
	if spec == nil {
		return
	}
	key := r.maintKey()
	start, end := r.maintBounds()
	r.setMaint(func(s *score.MaintenanceStats) { s.CR, s.Start, s.End = key, start, end })
	if slices.Contains(r.opts.SkipLanes, maintLane) {
		r.setMaint(func(s *score.MaintenanceStats) { s.Skipped = laneSkipped })
		return
	}
	ops := r.e.d.Maintenance
	if ops == nil {
		r.setMaint(func(s *score.MaintenanceStats) { s.Setup = "no front-office user credential (campaign-fo)" })
		return
	}
	if !r.e.clock.SleepUntil(ctx, r.t0.Add(spec.At)) {
		r.setMaint(func(s *score.MaintenanceStats) { s.Setup = "campaign ended before the lane" })
		return
	}
	ids, applied, err := r.maintSetup(ctx, ops, key, start, end)
	if err != nil || applied != "" { // an always-applied pattern joined: disable before it starts
		r.setMaint(func(s *score.MaintenanceStats) {
			s.Applied = applied
			if err != nil {
				s.Setup = err.Error()
			}
		})
		r.maintRemove(context.WithoutCancel(ctx), ids)
		return
	}
	r.maintEdgeRead(ctx, ops, key, "scheduled", r.now(), start.Add(-5*time.Second), start, end)

	var probes []score.MaintProbe
	post := func(kind string, n int, value, ticket string) {
		name := fmt.Sprintf("Campaign-%s-maint-%s-%d", r.id, kind, n)
		labels := map[string]string{"alertname": name, "campaign_run": r.id, "campaign_lane": maintLane,
			maintLabel: value, "severity": "warning", "create_ticket": ticket}
		at := r.now()
		_, _ = r.e.d.Target.PostAlert(ctx, Alert{Name: name, Labels: labels, StartsAt: at.Truncate(time.Second)})
		probes = append(probes, score.MaintProbe{Kind: kind, Name: name, PostedAt: at})
	}
	token := maintToken(r.id)
	activeAt, active := start.Add(maintEdge), false
	readActive := func() {
		active = true
		r.maintEdgeRead(ctx, ops, key, "active", activeAt, activeAt.Add(maintEdge), start, end)
	}
	n := 0
	for at := start.Add(5 * time.Second); !at.After(end.Add(-spec.Lag)); at = at.Add(spec.Every) {
		if !active && at.After(activeAt) {
			readActive()
		}
		if !r.e.clock.SleepUntil(ctx, at) {
			break
		}
		n++
		post(score.MaintInside, n, token, "snow")
		post(score.MaintControl, n, token+"-ctl", "false")
	}
	if !active {
		readActive()
	}
	r.maintEdgeRead(ctx, ops, key, "expired", end.Add(maintEdge), end.Add(maintEdge+maintEdge), start, end)
	last := end.Add(5 * time.Second)
	for i, at := 1, end.Add(5*time.Second); at.Before(end.Add(65 * time.Second)); i, at = i+1, at.Add(spec.Every) {
		if !r.e.clock.SleepUntil(ctx, at) {
			break
		}
		post(score.MaintAfter, i, token, "false")
		last = at
	}
	rows := r.maintReadBack(ctx, probes, last.Add(maintReadBudget))
	r.setMaint(func(s *score.MaintenanceStats) {
		s.Posted = len(probes)
		for i := range probes {
			if row, ok := rows[probes[i].Name]; ok {
				probes[i].Found, probes[i].Status, probes[i].Window, probes[i].CreatedAt = true, row.Status, row.Annotations["suppression_window"], row.CreatedAt
				if probes[i].Kind == score.MaintInside && row.CreatedAt.After(s.End) {
					s.Late++
				}
			}
		}
		s.Probes = probes
	})
	stable := context.WithoutCancel(ctx)
	removed := r.maintRemove(stable, ids)
	for _, row := range rows { // resolve the probes (cleanup re-checks every campaign row)
		if req, err := oscar.BuildHistoryResolve(row, r.id); err == nil {
			_, _ = r.e.d.Target.PostAlertGroup(stable, req)
		}
	}
	r.setMaint(func(s *score.MaintenanceStats) { s.Removed = removed })
}

// maintSetup creates the pattern, template and scheduled window, each
// recorded first, and returns the mutation ids (newest last).
func (r *run) maintSetup(ctx context.Context, ops MaintOps, key string, start, end time.Time) (ids []int64, applied string, err error) {
	step := func(kind string, create func() error) error {
		id, err := r.record(ctx, kind, key, map[string]string{"key": key})
		if err != nil {
			return fmt.Errorf("record %s: %w", kind, err)
		}
		ids = append(ids, id)
		if err := create(); err != nil {
			return fmt.Errorf("create %s: %w", strings.TrimPrefix(kind, "maint_"), err)
		}
		return nil
	}
	token := maintToken(r.id)
	if err := step(kindMaintPattern, func() error {
		return ops.CreateMaintPattern(ctx, targetops.MaintPattern{Key: key, Name: "CorrTest " + r.id, Description: "CorrTest maintenance lane; removed by its campaign",
			Match: []targetops.MaintMatch{{Label: maintLabel, Operator: "equals", Value: token}}, Combinator: "and"})
	}); err != nil {
		return ids, "", err
	}
	if err := step(kindMaintTemplate, func() error {
		return ops.CreateMaintTemplate(ctx, targetops.MaintTemplate{Key: key, Name: "CorrTest " + r.id, ServerSelector: map[string]any{}, Servers: []string{},
			Patterns: []string{key}, DefaultHandlers: []string{maintHandler}})
	}); err != nil {
		return ids, "", err
	}
	err = step(kindMaintWindow, func() error {
		en, err := ops.EnableMaint(ctx, targetops.MaintWindow{CR: key, Description: "CorrTest maintenance lane " + r.id, Servers: []string{},
			Patterns: []string{key}, Handlers: []string{maintHandler}, Timezone: "UTC", Start: start, End: end, TemplateKey: key})
		if err != nil {
			return err
		}
		if !slices.Equal(en.Patterns, []string{key}) || !slices.Equal(en.Handlers, []string{maintHandler}) {
			applied = fmt.Sprintf("patterns %v and handlers %v", en.Patterns, en.Handlers)
		}
		return nil
	})
	return ids, applied, err
}

// maintEdgeRead reads the window status at (or after) at and records it;
// a read after late is not judged.
func (r *run) maintEdgeRead(ctx context.Context, ops MaintOps, key, want string, at, late time.Time, start, end time.Time) {
	if !r.e.clock.SleepUntil(ctx, at) {
		return
	}
	st, err := ops.MaintStatus(ctx, key)
	e := score.MaintEdge{Want: want, Got: st.State, At: r.now()}
	if err != nil {
		e.Got = "error: " + err.Error()
	}
	e.Late = e.At.After(late)
	if err == nil && st.Start != nil && st.End != nil && (!st.Start.Equal(start) || !st.End.Equal(end)) {
		e.Bounds = st.Start.UTC().Format(time.RFC3339) + " to " + st.End.UTC().Format(time.RFC3339)
	}
	r.setMaint(func(s *score.MaintenanceStats) { s.Edges = append(s.Edges, e) })
}

// maintReadBack polls History until every probe has a row or the deadline,
// and returns the rows by alert name.
func (r *run) maintReadBack(ctx context.Context, probes []score.MaintProbe, deadline time.Time) map[string]oscar.HistoryRecord {
	rows := map[string]oscar.HistoryRecord{}
	if len(probes) == 0 {
		return rows
	}
	from := probes[0].PostedAt.Add(-time.Minute)
	for {
		hist, err := r.e.d.Target.History(context.WithoutCancel(ctx), r.id, from, r.now().Add(time.Minute))
		if err == nil {
			for _, h := range hist {
				if h.Labels["campaign_lane"] == maintLane {
					rows[h.AlertName] = h
				}
			}
		}
		if len(rows) >= len(probes) || !r.now().Before(deadline) || !r.e.clock.SleepUntil(ctx, r.now().Add(maintPoll)) {
			return rows
		}
	}
}

// maintRemove restores the lane's mutations newest first (window, template,
// pattern) and reports whether all were removed and read back.
func (r *run) maintRemove(ctx context.Context, ids []int64) bool {
	ok := true
	for i := len(ids) - 1; i >= 0; i-- {
		if err := r.restoreNow(ctx, ids[i]); err != nil {
			ok = false
			r.event(ctx, "campaign.maintenance", "warn", "remove: "+err.Error(), nil)
		}
	}
	return ok && len(ids) == 3
}

// restoreMaint removes one maintenance object and verifies it is gone.
func (r *run) restoreMaint(ctx context.Context, m domain.CampaignMutation) error {
	ops := r.e.d.Maintenance
	if ops == nil {
		return fmt.Errorf("no maintenance client to remove %s %s", m.Kind, m.ObjectID)
	}
	switch m.Kind {
	case kindMaintWindow:
		if err := ops.DisableMaint(ctx, m.ObjectID); err != nil {
			return err
		}
		return maintWindowGone(ctx, ops, m.ObjectID)
	case kindMaintTemplate:
		if err := ops.DeleteMaintTemplate(ctx, m.ObjectID); err != nil {
			return err
		}
		all, err := ops.MaintTemplates(ctx)
		if err != nil {
			return err
		}
		for _, t := range all {
			if t.Key == m.ObjectID {
				return fmt.Errorf("template %s still listed", m.ObjectID)
			}
		}
		return nil
	default: // kindMaintPattern
		if err := ops.DeleteMaintPattern(ctx, m.ObjectID); err != nil {
			return err
		}
		all, err := ops.MaintPatterns(ctx)
		if err != nil {
			return err
		}
		for _, p := range all {
			if p.Key == m.ObjectID {
				return fmt.Errorf("pattern %s still listed", m.ObjectID)
			}
		}
		return nil
	}
}

// maintWindowGone is the removal read-back: OSCAR holds no window for cr (an
// expired one still has its rules), and its ledger rows report the live
// window absent.
func maintWindowGone(ctx context.Context, ops MaintOps, cr string) error {
	st, err := ops.MaintStatus(ctx, cr)
	if err != nil {
		return err
	}
	if !st.Absent() {
		return fmt.Errorf("window %s still present (%s)", cr, st.State)
	}
	recs, err := ops.MaintRecords(ctx, cr)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.CR != cr {
			continue
		}
		live, err := ops.MaintLive(ctx, rec.ID)
		if err != nil {
			return err
		}
		if live != "absent" {
			return fmt.Errorf("window %s ledger %s live_status %q", cr, rec.ID, live)
		}
	}
	return nil
}

// maintScore puts the Maintenance section on the card and judges it; the
// inside probes' sink receipts and incidents come from the run's evidence.
func (r *run) maintScore(card *score.Scorecard, receipts []targetops.Receipt, incidents []targetops.Incident, report map[string]any) {
	if r.s.Maintenance == nil {
		return
	}
	r.mu.Lock()
	m := r.maint
	m.Probes = append([]score.MaintProbe(nil), r.maint.Probes...)
	m.Edges = append([]score.MaintEdge(nil), r.maint.Edges...)
	r.mu.Unlock()
	for i, p := range m.Probes {
		if p.Kind != score.MaintInside {
			continue
		}
		for _, inc := range incidents {
			if mentions(inc.ShortDescription, p.Name) {
				m.Probes[i].Ticketed = true
			}
		}
		for _, rc := range receipts {
			for _, a := range rc.Alerts {
				if a.AlertName == p.Name {
					m.SinkReceipts++
				}
			}
		}
	}
	if m.Skipped != "" {
		card.SkippedLanes = append(card.SkippedLanes, maintLane+": "+m.Skipped)
	}
	card.Maintenance = &m
	report["maintenance"] = m
	fails, incon := m.Judge()
	judge(card, fails, incon)
}
