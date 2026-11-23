package engine

import (
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
)

type dispatchWindow struct{ Before, By time.Time }
type dispatchSamples struct {
	Previous        time.Time
	ExpectedDue     map[string]time.Time
	Windows         map[string]dispatchWindow
	ValidUntil      time.Time
	ValiditySamples int
	Queries         int
	Elapsed         time.Duration
}

func (d *dispatchSamples) add(s managed.Snapshot) {
	if d.Windows == nil {
		d.Windows = map[string]dispatchWindow{}
	}
	for _, root := range s.DispatchRoots {
		if _, ok := d.Windows[root]; !ok {
			d.Windows[root] = dispatchWindow{d.Previous, s.MeasuredAt}
		}
	}
	d.Previous = s.MeasuredAt.Add(-s.QueryDuration)
	d.Queries++
	d.Elapsed += s.QueryDuration
}
func ownedBinding(p *policyState, bindings []managed.Binding) (managed.Binding, error) {
	var result managed.Binding
	for _, b := range bindings {
		if b.Notifier != p.notifierID || b.Action != p.spec.Action {
			continue
		}
		if result.ID != "" || b.ID == "" || b.Revision == "" || b.Rule != p.id || b.CatalogRevision != p.rev || b.State != "bound" {
			return result, errors.New("wrong or ambiguous immutable target binding")
		}
		result = b
	}
	if result.ID == "" {
		return result, errors.New("missing owned target binding")
	}
	return result, nil
}

type slotClock struct {
	receiver     string
	active, last time.Time
	source       string
	raised       bool
}

func (c slotClock) next(p *policyState) (time.Time, string, string) {
	last := c.last
	if last.IsZero() {
		last = c.active
	}
	due := last.Add(p.spec.Interval)
	sev := c.source
	if esc := p.spec.Escalation; esc != nil {
		raise := c.active.Add(esc.After)
		if !c.raised && !raise.After(due) {
			return raise, "escalate", esc.TargetSeverity
		}
		if c.raised {
			sev = esc.TargetSeverity
		}
	}
	return due, "resend", sev
}
func routedScope(e managed.Effect, notifier, action string) bool {
	if e.Kind == "escalate" {
		return e.CommandScopeSize != nil && *e.CommandScopeSize == 0 && e.ScopeSize != nil && *e.ScopeSize == 0
	}
	return e.ScopeNotifier == notifier && e.ScopeAction == action
}

func (c *slotClock) accept(p *policyState, b managed.Binding, e managed.Effect, d dispatchWindow) error {
	due, kind, sev := c.next(p)
	if e.ScheduleRoot == "" || e.ScheduleRoot != e.Logical || e.Target == "" || c.receiver == "" || e.Receiver != c.receiver || !routedScope(e, p.notifierID, p.spec.Action) || e.Callback == "" || e.Operation == "" || e.ChildState != "sent" || e.Child == "" || e.TargetState != "confirmed" || e.Action != p.spec.Action || e.Notifier != p.notifierID || e.BindingID != b.ID || e.Revision != b.Revision || e.Kind != kind || e.Severity != sev {
		return errors.New("scheduled effect differs from expected kind, severity, target or immutable binding")
	}
	if d.Before.IsZero() || d.Before.Before(due) || d.By.Before(d.Before) {
		return errors.New("dispatch observation overlaps earliest deadline or lacks prior sample")
	}
	if err := resend.CheckDispatch(due, time.Time{}, d.By, 0); err != nil {
		return err
	}
	cb, err := time.Parse(time.RFC3339Nano, e.ReceivedAt)
	if err != nil || cb.Before(d.Before) {
		return errors.New("invalid callback observation")
	}
	end, err := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
	if err != nil || end.Before(cb) {
		return errors.New("invalid provider confirmation")
	}
	c.last = end
	if kind == "escalate" {
		c.raised = true
	}
	return nil
}
