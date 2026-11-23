package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

type closedMember struct {
	Receipt                                int64
	Start, Severity, Correlation, Notifier string
}

func capturedIdentity(r targetops.Receipt, a targetops.SinkAlert) closedMember {
	return closedMember{r.ID, a.StartsAt, a.Labels["severity"], a.Labels["correlation_key"], r.Notifier}
}

type closedSubject struct {
	AID, Condition string
	Fixture        NativeEpisodeFixture
	Roots          map[string][32]byte
	Receipts       map[closedMember]bool
}

func signatures(rs map[string]managed.OriginalReceipt) map[string][32]byte {
	result := map[string][32]byte{}
	for root, original := range rs {
		result[root] = sha256.Sum256(mustJSON(original.Children))
	}
	return result
}
func (m *managedLane) rememberClosed(aid, cond string, f NativeEpisodeFixture, originals map[string]managed.OriginalReceipt, rs []targetops.Receipt) {
	v := closedSubject{aid, cond, f, signatures(originals), firingIDs(aid, rs)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed == nil {
		m.closed = map[string]closedSubject{}
	}
	m.closed[aid] = v
}

// Original reads accept the logical root itself as the seed operation. They
// return every descendant and manifest child, including superseded operations.
func scheduledRoots(s managed.Snapshot) map[string]bool {
	roots := map[string]bool{}
	for _, d := range s.Dispatches {
		roots[d.Root] = true
	}
	for _, e := range s.Effects {
		roots[e.Logical] = true
	}
	return roots
}
func firingIDs(aid string, rs []targetops.Receipt) map[closedMember]bool {
	ids := map[closedMember]bool{}
	for _, r := range rs {
		for _, a := range r.Alerts {
			if a.Labels["aid"] == aid && a.Status == "firing" {
				ids[capturedIdentity(r, a)] = true
			}
		}
	}
	return ids
}

// One paginated capture per run-owned condition. Compare closed members through
// queue drain so a late physical effect cannot disappear after a rotation.
func (m *managedLane) verifyDrain(ctx context.Context) error {
	if len(m.closed) == 0 {
		return errors.New("no closed policy/rate subjects")
	}
	var problems []error
	byCond := map[string]map[string]map[closedMember]bool{}
	for _, v := range m.closed {
		if _, ok := byCond[v.Condition]; ok {
			continue
		}
		rs, err := m.sink.ManagedConditionReceipts(ctx, v.Condition, m.run.t0)
		if err != nil {
			problems = append(problems, err)
			byCond[v.Condition] = nil
			continue
		}
		ids := map[string]map[closedMember]bool{}
		for _, r := range rs {
			for _, a := range r.Alerts {
				if a.Status != "firing" {
					continue
				}
				aid := a.Labels["aid"]
				if a.Labels["node"] != m.config.TID || a.Labels["source_id"] != m.config.SourceID {
					problems = append(problems, errors.New("foreign member in owned condition capture"))
					continue
				}
				if ids[aid] == nil {
					ids[aid] = map[closedMember]bool{}
				}
				if ids[aid][capturedIdentity(r, a)] {
					problems = append(problems, errors.New("duplicate physical member in final capture"))
					continue
				}
				ids[aid][capturedIdentity(r, a)] = true
			}
		}
		byCond[v.Condition] = ids
	}
	for _, v := range m.closed {
		if !maps.Equal(byCond[v.Condition][v.AID], v.Receipts) {
			problems = append(problems, fmt.Errorf("final receipt set changed for %s", v.AID))
		}
		snap, err := m.config.Snapshot(ctx, v.Fixture.History.ID)
		if err != nil {
			problems = append(problems, err)
			for root := range v.Roots {
				problems = append(problems, m.retainRoot(ctx, v.Fixture, root))
			}
			continue
		}
		originals, err := m.proveClosedRoots(ctx, v.Fixture, snap, m.policy.notifierID)
		if err != nil {
			problems = append(problems, err)
		}
		roots := scheduledRoots(snap)
		for root := range v.Roots {
			if !roots[root] {
				problems = append(problems, m.retainRoot(ctx, v.Fixture, root))
			}
		}
		if !maps.Equal(v.Roots, signatures(originals)) {
			problems = append(problems, fmt.Errorf("final original operations or children changed for %s", v.AID))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return err
	}
	return m.save(ctx, "native.managed.drain", map[string]any{"closedSubjects": len(m.closed), "checkedAt": m.run.now()})
}

func (m *managedLane) proveClosedRoots(ctx context.Context, f NativeEpisodeFixture, s managed.Snapshot, notifier string) (map[string]managed.OriginalReceipt, error) {
	originals := map[string]managed.OriginalReceipt{}
	var problems []error
	for root := range scheduledRoots(s) {
		original, err := m.originals.Original(ctx, f.History.ID, root)
		settle, ok := original.Settlement(f.History.ID, f.History.EpisodeNumber, root)
		if err != nil || !ok {
			problems = append(problems, fmt.Errorf("unsettled original %s", root), err, m.retainRoot(ctx, f, root))
			continue
		}
		for _, child := range settle.Children {
			if child.Action != "ticket" || child.State != "sent" {
				continue
			}
			owner := originalOwner{Kind: "unexpected_ticket", History: f.History.ID, Episode: f.History.EpisodeNumber, AID: f.History.Labels["aid"], Root: root, Operation: child.Operation, Target: child.Target, Child: child.Child}
			_, recordErr := m.run.record(context.WithoutCancel(ctx), kindManagedOwner, child.Child, owner)
			problems = append(problems, recordErr, m.save(context.WithoutCancel(ctx), "native.unexpected_ticket", owner))
		}
		if err = checkNoTicketRoot(settle, notifier, m.run.notifiers); err != nil {
			problems = append(problems, err)
		}
		if err = matchOriginalEffects(settle, s.Effects); err != nil {
			problems = append(problems, err)
		}
		originals[root] = original
	}
	return originals, errors.Join(problems...)
}
func (m *managedLane) retainRoot(ctx context.Context, f NativeEpisodeFixture, root string) error {
	_, err := m.run.record(context.WithoutCancel(ctx), kindManagedOwner, root, originalOwner{Kind: "segment_root", AID: f.History.Labels["aid"], History: f.History.ID, Episode: f.History.EpisodeNumber, Root: root, Operation: root})
	return err
}
func checkNoTicketRoot(s managed.Settlement, sink string, notifiers map[string]string) error {
	history := notifiers["ALERTHISTORY"]
	notifications, tickets := 0, 0
	ordinary := false
	for _, c := range s.Children {
		if c.Successor != "" {
			continue
		}
		ordinary = ordinary || c.Kind == "ordinary"
		switch {
		case c.Action == "ticket" && strings.EqualFold(c.Name, "ALERTHISTORY") && history != "" && c.Notifier == history && c.State == "suppressed":
			tickets++
		case c.Kind == "ordinary" && c.Action == "notification" && strings.EqualFold(c.Name, "LOGGER") && c.Notifier != "" && c.Notifier == notifiers["LOGGER"] && c.State == "sent":
		case c.Action == "notification" && c.Notifier == sink && c.State == "sent":
			notifications++
		default:
			return errors.New("unexpected original child or outcome on no-ticket subject")
		}
	}
	if tickets > 1 || ordinary && tickets != 1 {
		return errors.New("ordinary no-ticket original lacks its skipped History child")
	}
	if notifications != 1 {
		return errors.New("original lacks one commissioned sink child")
	}
	return nil
}
func matchOriginalEffects(s managed.Settlement, effects []managed.Effect) error {
	for _, c := range s.Children {
		if c.Action != "notification" || c.Successor != "" {
			continue
		}
		matches := 0
		for _, e := range effects {
			if e.Child == c.Child && e.Operation == c.Operation && e.Logical == c.Root && e.Callback == c.Callback && e.Target == c.Target && e.Notifier == c.Notifier && e.ChildState == "sent" && e.ConfirmedAt != "" {
				matches++
			}
		}
		if matches != 1 {
			return errors.New("original sink child lacks an attributable effect")
		}
	}
	return nil
}
