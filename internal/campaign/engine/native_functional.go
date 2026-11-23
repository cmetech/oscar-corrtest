package engine

import (
	"encoding/json"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func (t *nativeTally) functional(ev domain.CampaignEvent) bool {
	r := t.run
	switch ev.Type {
	case "native.tl1.closed":
		var d struct{ Verified bool }
		return json.Unmarshal(ev.Detail, &d) == nil && d.Verified
	case "native.managed.functional":
		var d struct{ Completed bool }
		return json.Unmarshal(ev.Detail, &d) == nil && d.Completed
	case "native.source_observed":
		var f NativeEpisodeFixture
		if json.Unmarshal(ev.Detail, &f) != nil || f.History.ID == "" || f.History.EpisodeNumber < 1 || f.Source.History != f.History.ID || f.Source.Authority == "" || f.Source.Episode == "" || f.Source.Variant < 1 {
			return false
		}
		if f.History.Labels["aid"] == managed.AID(r.id, "main", 0) {
			switch {
			case t.initial.History.ID == "" && f.Source.SourceSeverity == "minor":
				t.initial = f
			case f.Source.SourceSeverity == "major" && t.changed.History.ID == "":
				a, b := t.initial.Source, f.Source
				if a.Episode != b.Episode || a.History != b.History || a.Authority != b.Authority || a.Generation != b.Generation || b.Variant <= a.Variant || b.EventID == a.EventID {
					return false
				}
				t.changed = f
			case f.Source.SourceSeverity == "major" && f.Source.Episode != t.changed.Source.Episode && f.Source.ManagedKey != t.changed.Source.ManagedKey && f.Source.Floor == "":
				t.fresh = f
			}
		}
	case "native.action.children":
		return json.Unmarshal(ev.Detail, &t.original) == nil && len(t.original.Children) > 0
	case "native.action.status":
		var status managed.CommandReceipt
		if json.Unmarshal(ev.Detail, &status) != nil {
			return false
		}
		f := t.changed
		proof, ok := t.original.Resend(status, f.History.ID, f.History.EpisodeNumber, t.request, status.ResendID)
		return ok && proof.DisabledTicketNotification(r.sinkID) != ""
	case "native.action.intent":
		var d struct{ History, Kind, Request string }
		if json.Unmarshal(ev.Detail, &d) != nil {
			return false
		}
		if d.Kind == "manual_resend" && d.History == t.changed.History.ID {
			t.request = d.Request
		}
	case "native.managed.refresh":
		var d struct {
			History string
			Member  lane.NativeMember
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.History != t.changed.History.ID {
			return false
		}
		deadline, err := time.Parse(time.RFC3339Nano, t.changed.Source.RefreshDeadline)
		return err == nil && d.Member.EndsAt.After(deadline) && observedMember(d.Member, t.changed.Source, "critical", ev.OccurredAt)
	case "native.acknowledged.repeat":
		var d struct {
			Subject NativeEpisodeFixture
			Effect  managed.Effect
			Sink    []targetops.Receipt
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Subject.History.ID != t.changed.History.ID || d.Effect.Kind != "resend" || d.Effect.Severity != "critical" || d.Effect.Notifier != r.sinkID || d.Effect.ChildState != "sent" || len(d.Sink) == 0 {
			return false
		}
		t.receipts(d.Sink, d.Subject.History.Labels["aid"])
	case "native.resolved.eligibility":
		var d managed.Eligibility
		if json.Unmarshal(ev.Detail, &d) != nil || d.HistoryID != t.changed.History.ID || d.Episode != t.changed.History.EpisodeNumber {
			return false
		}
		for _, k := range []string{"resend", "acknowledge", "ticket_create"} {
			v, ok := d.Actions[k]
			if !ok || v.Allowed || v.Reason != "NATIVE_EPISODE_RESOLVED" {
				return false
			}
		}
	case "native.bulk.isolation":
		var d struct {
			Original, Refused                 managed.CommandReceipt
			Children                          managed.OriginalReceipt
			Selected, Sibling                 NativeEpisodeFixture
			Effects                           []managed.Effect
			SelectedReceipts, SiblingReceipts []targetops.Receipt
		}
		if json.Unmarshal(ev.Detail, &d) != nil || t.fresh.History.ID == "" || d.Selected.History.ID != t.fresh.History.ID || d.Refused.AlertID != t.changed.History.ID || d.Refused.Error.Code != "NATIVE_EPISODE_RESOLVED" {
			return false
		}
		proof, ok := d.Children.Resend(d.Original, d.Selected.History.ID, d.Selected.History.EpisodeNumber, d.Original.RequestID, d.Original.ResendID)
		child := proof.DisabledTicketNotification(r.sinkID)
		if !ok || child == "" {
			return false
		}
		ok, err := bulkIsolation(d.Selected, d.Sibling, d.Original.ResendID, r.sinkID, child, d.Effects, d.SelectedReceipts, d.SiblingReceipts)
		return ok && err == nil
	case "native.synthetic_ticket":
		var d struct {
			Run, History, Ticket, ProviderID string
			Eligibility                      managed.Eligibility
			Bindings                         []managed.Binding
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Run == "" || d.History == "" || d.ProviderID == "" || d.Ticket == "" || d.Eligibility.HistoryID != d.History {
			return false
		}
		for _, k := range []string{"resend", "acknowledge", "ticket_create"} {
			v, ok := d.Eligibility.Actions[k]
			if !ok || v.Allowed || v.Reason != "SYNTHETIC_ACTION_DISABLED" {
				return false
			}
		}
		for _, b := range d.Bindings {
			if b.Rule != "" || b.Revision != "" {
				return false
			}
		}
	case "native.suppression_released":
		var d struct {
			Control                                    string
			Binding                                    managed.Binding
			ReleaseByNS, DispatchAfterNS, DispatchByNS int64
			Effects                                    []managed.Effect
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Control == "" || d.Binding.Revision == "" || len(d.Effects) == 0 {
			return false
		}
		return d.ReleaseByNS >= 0 && d.DispatchAfterNS >= d.ReleaseByNS && d.DispatchByNS >= d.DispatchAfterNS && d.DispatchByNS <= int64(30*time.Second)
	}
	return true
}
