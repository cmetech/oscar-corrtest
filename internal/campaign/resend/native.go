package resend

import (
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

type SourceRef struct {
	Scope, ProblemKey, Episode, Starts string
	Generation                         int64
}
type NativeEvent struct {
	Kind, Severity, Target, Token, Outcome   string
	At                                       time.Time
	Source                                   SourceRef
	Fresh, Suppressed, Synthetic, AutoTicket bool
}
type NativeExpected struct {
	Due                                   time.Time
	Kind, Severity, Target, TicketOutcome string
}
type NativeClock struct {
	Policy                          scenario.PolicySpec
	Target                          string
	Source                          SourceRef
	Start, LastContact              time.Time
	SourceSeverity, Floor           string
	Count, Used                     int
	Resolved, Suppressed, Synthetic bool
	Pending                         string
	pendingKind                     string
}

var rank = map[string]int{"info": 1, "minor": 2, "warning": 3, "major": 4, "critical": 5}

func (c *NativeClock) effective() string {
	if rank[c.Floor] > rank[c.SourceSeverity] {
		return c.Floor
	}
	return c.SourceSeverity
}

// Apply consumes independently supplied source lifecycle and target confirmations.
// timer_due is an opportunity to inspect elapsed time, never a claim that a send happened.
func (c *NativeClock) Apply(e NativeEvent) (*NativeExpected, error) {
	expect := func(due time.Time, kind, sev, ticket string) (*NativeExpected, error) {
		if e.Token == "" {
			return nil, errors.New("expected action requires original token")
		}
		c.Pending = e.Token
		c.pendingKind = kind
		return &NativeExpected{Due: due, Kind: kind, Severity: sev, Target: c.Target, TicketOutcome: ticket}, nil
	}
	if e.Kind == "new_episode" {
		if c.Source.Episode != "" && (!c.Resolved || c.Source == e.Source || c.Pending != "") {
			return nil, errors.New("prior episode unresolved or original operation unsettled")
		}
		if e.At.IsZero() || e.Source.Scope == "" || e.Source.Episode == "" || e.Source.Starts == "" || e.Source.Generation < 1 || rank[e.Severity] == 0 {
			return nil, errors.New("unproved source episode")
		}
		*c = NativeClock{Policy: c.Policy, Target: c.Target, Source: e.Source, Start: e.At, SourceSeverity: e.Severity, Count: 1, Synthetic: e.Synthetic}
		if e.Synthetic && e.AutoTicket {
			return expect(e.At, "automatic_ticket", e.Severity, "handler_mapped")
		}
		return nil, nil
	}
	if c.Source.Episode == "" || e.Source != c.Source {
		return nil, errors.New("unproved episode continuity")
	}
	switch e.Kind {
	case "source_observed":
		if c.Resolved || rank[e.Severity] == 0 {
			return nil, errors.New("invalid source observation")
		}
		c.SourceSeverity = e.Severity
		if e.Fresh {
			c.Count++
		}
	case "resolved":
		c.Resolved = true
	case "suppression_changed":
		c.Suppressed = e.Suppressed
	case "provider_outcome":
		if e.Target != c.Target || e.Token == "" || c.Pending != e.Token {
			return nil, errors.New("provider evidence does not own this target and operation")
		}
		switch e.Outcome {
		case "unknown":
			return nil, nil
		case "confirmed":
			c.LastContact = e.At
			if c.pendingKind == "escalation" {
				c.Floor = c.Policy.Escalation.TargetSeverity
			}
			if c.pendingKind == "repeat" {
				c.Used++
			}
		case "no_effect":
		default:
			return nil, errors.New("invalid provider outcome")
		}
		c.Pending = ""
		c.pendingKind = ""
	case "initial_contact", "manual_resend", "manual_ticket", "timer_due":
		if c.Resolved || c.Pending != "" || c.Synthetic || c.Suppressed && e.Kind != "manual_ticket" {
			return nil, nil
		}
		if e.Kind != "timer_due" {
			return expect(e.At, e.Kind, c.effective(), "")
		}
		esc := c.Policy.Escalation
		if esc != nil && rank[c.effective()] < rank[esc.TargetSeverity] && !e.At.Before(c.Start.Add(esc.After)) {
			return expect(c.Start.Add(esc.After), "escalation", esc.TargetSeverity, "handler_mapped")
		}
		if !c.LastContact.IsZero() && c.Policy.Interval > 0 && (c.Policy.MaxResends == nil || c.Used < *c.Policy.MaxResends) && !e.At.Before(c.LastContact.Add(c.Policy.Interval)) {
			return expect(c.LastContact.Add(c.Policy.Interval), "repeat", c.effective(), "")
		}
	default:
		return nil, errors.New("unknown lifecycle event")
	}
	return nil, nil
}
func CheckDispatch(due, eligible, dispatch time.Time, measuredSkew time.Duration) error {
	if due.IsZero() || dispatch.IsZero() || measuredSkew < 0 || measuredSkew > 250*time.Millisecond {
		return errors.New("missing dispatch or qualified clock evidence")
	}
	if eligible.After(due) {
		due = eligible
	}
	if dispatch.Before(due.Add(-measuredSkew)) || dispatch.After(due.Add(30*time.Second)) {
		return errors.New("dispatch outside qualified due window")
	}
	return nil
}
