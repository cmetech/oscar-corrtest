package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type wireCounts struct{ Offered, Written, Unknown, NotEntered, DuringLoad int }
type nativeEvidence struct {
	SourceOffers                               map[string]sourceOffers
	TotalOfferedInLoad                         int
	OverallOfferedRate, OverallTL1OfferedShare float64

	Facets                                                            map[string]int
	Wire                                                              map[string]*wireCounts
	PhysicalRequests, ProviderMemberReceipts, RateSlots               int
	PlannedBaselineInLoad, PlannedRatePDUs, AcceptedBaselineInLoad    int
	RequestedShare, ConfiguredShare, AchievedShare                    float64
	LoadedSeconds, OfferedBaselinePerSecond, WrittenRatePDUsPerSecond float64
	Missing, Failures                                                 []string
}
type nativeTally struct {
	timing                  map[string][]float64
	initial, changed, fresh NativeEpisodeFixture
	original                managed.OriginalReceipt
	request                 string
	nativeEvidence
	run             *run
	intents, writes map[[3]string]int
	sequences       map[uint64]bool
	slots           map[int]bool
	segments        map[string]bool
	ordinary        map[string]lane.NativeMember
	wireOrdinary    map[[3]string]int
	physical        map[int64]bool
	members         map[string]bool
}

func newNativeTally(r *run) *nativeTally {
	return &nativeTally{timing: map[string][]float64{}, run: r, nativeEvidence: nativeEvidence{SourceOffers: map[string]sourceOffers{}, Facets: map[string]int{}, Wire: map[string]*wireCounts{}}, intents: map[[3]string]int{}, writes: map[[3]string]int{}, sequences: map[uint64]bool{}, slots: map[int]bool{}, segments: map[string]bool{}, ordinary: map[string]lane.NativeMember{}, wireOrdinary: map[[3]string]int{}, physical: map[int64]bool{}, members: map[string]bool{}}
}
func (t *nativeTally) miss(what string) {
	if !slices.Contains(t.Missing, what) {
		t.Missing = append(t.Missing, what)
	}
}
func (t *nativeTally) fail(what string) {
	if !slices.Contains(t.Failures, what) {
		t.Failures = append(t.Failures, what)
	}
}
func (t *nativeTally) wire(cohort string) *wireCounts {
	if t.Wire[cohort] == nil {
		t.Wire[cohort] = &wireCounts{}
	}
	return t.Wire[cohort]
}
func (t *nativeTally) consume(ev domain.CampaignEvent) {
	if !strings.HasPrefix(ev.Type, "native.") {
		return
	}
	if !json.Valid(ev.Detail) {
		t.miss("unreadable " + ev.Type)
		return
	}
	r := t.run
	if !t.functional(ev) {
		t.miss("invalid evidence for " + ev.Type)
		return
	}
	switch ev.Type {
	case "native.tl1.intent", "native.tl1.wire":
		var e managed.WireEvent
		if json.Unmarshal(ev.Detail, &e) != nil || e.AID == "" || e.Condition == "" || e.Code == "" {
			t.miss("invalid TL1 input evidence")
			return
		}
		cohort := "functional"
		if strings.HasPrefix(e.AID, strings.TrimSuffix(managed.AID(r.id, "mixed", 0), "000000")) {
			cohort = "mixed_rate"
		} else if strings.HasPrefix(e.AID, strings.TrimSuffix(managed.AID(r.id, "standing", 0), "-000000")+"-") {
			cohort = "segment"
		}
		k := [3]string{e.AID, e.Condition, e.Code}
		counts := t.wire(cohort)
		if ev.Type == "native.tl1.intent" {
			t.intents[k]++
			counts.Offered++
			return
		}
		t.writes[k]++
		status := 0
		if e.Outcome == "written" {
			status = 200
		}
		t.offer("tl1_"+cohort, e.At, status)
		switch e.Outcome {
		case "written":
			counts.Written++
			if e.Sequence == 0 || t.sequences[e.Sequence] {
				t.fail("duplicate/empty wire sequence")
			}
			t.sequences[e.Sequence] = true
			if !e.At.Before(r.t0) && e.At.Before(r.t0.Add(r.s.LoadEnd)) {
				counts.DuringLoad++
			}
		case "unknown":
			counts.Unknown++
		default:
			counts.NotEntered++
		}
	case "native.ordinary.wire":
		var d struct {
			Source        lane.Event
			Route, Cohort string
			OfferedAt     time.Time
			HTTPStatus    int
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Source.Identity == "" {
			t.miss("invalid ordinary wire evidence")
			return
		}
		t.offer("ordinary_"+d.Route, d.OfferedAt, d.HTTPStatus)
		counts := t.wire("ordinary_" + d.Route)
		counts.Offered++
		if d.HTTPStatus/100 == 2 {
			counts.Written++
			t.wireOrdinary[[3]string{d.Source.Identity, d.Route, d.Cohort}]++
		} else {
			t.miss("ordinary PDU unaccepted")
		}
	case "native.ordinary.observed":
		var d struct {
			Identity, Route string
			Member          lane.NativeMember
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Member.Labels["alertname"] != d.Identity || d.Member.Labels["campaign_run"] != r.id || d.Member.Labels["campaign_lane"] != "native-"+d.Route || len(d.Member.Receivers) != 1 || d.Member.Receivers[0].Name == "" || d.Member.Fingerprint == "" || !d.Member.EndsAt.After(ev.OccurredAt) {
			t.miss("ordinary source identity, receiver or lifetime")
			return
		}
		t.ordinary[d.Identity] = d.Member
	case "native.managed.segment", "native.managed.segment_closed":
		var d struct {
			Snapshot              managed.Snapshot
			Dispatch              dispatchSamples
			Index                 int
			Lane, Reason          string
			Start, End, ClearedAt time.Time
			Fixture               NativeEpisodeFixture
			Originals             map[string]managed.OriginalReceipt
			Receipts              []targetops.Receipt
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Index < 0 || d.Lane != "POL" && d.Lane != "REPEAT" || d.Reason != "" || d.Fixture.History.ID == "" || d.Fixture.History.Labels["aid"] != managed.AID(r.id, "standing-"+d.Lane, d.Index) || !d.Start.Equal(r.t0.Add(time.Duration(d.Index)*r.s.NativeWorkload.ManagedSegment)) || !d.End.Equal(d.Start.Add(r.s.NativeWorkload.ManagedSegment)) || len(d.Receipts) == 0 {
			t.miss("malformed or unproved segment")
			return
		}
		if ev.Type == "native.managed.segment_closed" {
			if len(d.Originals) == 0 || d.ClearedAt.Before(d.End) {
				t.miss("original segment closure")
				return
			}
			t.receipts(d.Receipts, d.Fixture.History.Labels["aid"])
			for _, dispatch := range d.Snapshot.Dispatches {
				for _, e := range d.Snapshot.Effects {
					if e.ScheduleRoot != dispatch.Root || e.Notifier != r.sinkID || e.ChildState != "sent" {
						continue
					}
					due, err := time.Parse(time.RFC3339Nano, dispatch.DueAt)
					if err != nil {
						t.miss("segment timer due timestamp")
						continue
					}
					t.timed("segment_"+d.Lane, due, d.Dispatch.Windows[e.Logical], e)
				}
			}
		}
		key := fmt.Sprintf("%s/%s/%d", ev.Type, d.Lane, d.Index)
		if t.segments[key] {
			t.fail("duplicate " + key)
		}
		t.segments[key] = true
	case "native.managed.rate_slot":
		var d struct {
			Slot         int
			AID          string
			History      observedHistory
			Minor, Major managed.SourceReceipt
			Receipts     []targetops.Receipt
			Originals    map[string]managed.OriginalReceipt
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Slot < 0 || d.AID != managed.AID(r.id, "mixed", d.Slot) || d.History.ID == "" || d.History.Status != "resolved" || d.Major.History != d.History.ID || d.Minor.Episode == "" || d.Major.Episode != d.Minor.Episode || len(d.Receipts) == 0 || len(d.Originals) == 0 {
			t.miss("missing mixed slot downstream evidence")
			return
		}
		if t.slots[d.Slot] {
			t.fail("duplicate mixed slot")
		}
		t.slots[d.Slot] = true
		t.receipts(d.Receipts, d.AID)
	case "native.ticket":
		var d struct{ AID, Incident, SysID, Priority string }
		var raw map[string]json.RawMessage
		if json.Unmarshal(ev.Detail, &d) != nil || json.Unmarshal(ev.Detail, &raw) != nil || json.Unmarshal(raw["sys_id"], &d.SysID) != nil || d.Priority == "" || d.Incident == "" || d.SysID == "" {
			t.miss("managed ticket provider identity/priority")
			return
		}
		t.Facets["ticket/"+d.AID]++
	case "native.timer_due":
		var d struct {
			ExpectedKind, ExpectedSeverity string
			ExpectedDue                    time.Time
			Effect                         managed.Effect
			Dispatch                       dispatchWindow
		}
		if json.Unmarshal(ev.Detail, &d) != nil || d.Effect.Child == "" || d.ExpectedDue.IsZero() || d.Dispatch.Before.Before(d.ExpectedDue) || d.Dispatch.By.Before(d.Dispatch.Before) || d.Dispatch.By.After(d.ExpectedDue.Add(30*time.Second)) {
			t.miss("timer admission window")
			return
		}
		t.timed("functional", d.ExpectedDue, d.Dispatch, d.Effect)
		t.Facets["timer/"+d.ExpectedKind+"/"+d.ExpectedSeverity]++
	case "native.suppression_released", "native.suppression_changed":
		var d struct{ Kind, Control string }
		if json.Unmarshal(ev.Detail, &d) != nil || d.Control == "" {
			t.miss("suppression control identity")
			return
		}
		t.Facets[ev.Type+"/"+d.Kind]++
	}
	t.Facets[ev.Type]++
}
func (t *nativeTally) receipts(rs []targetops.Receipt, aid string) {
	for _, r := range rs {
		if r.ID <= 0 {
			t.miss("missing physical sink receipt ID")
			continue
		}
		for _, a := range r.Alerts {
			if a.Labels["aid"] == aid || a.AlertName == aid {
				t.physical[r.ID] = true
				t.members[fmt.Sprintf("%d/%s", r.ID, aid)] = true
			}
		}
	}
}
func (t *nativeTally) finish() {
	r := t.run
	w := r.s.NativeWorkload
	if t.Facets["native.drain.idle"] != 1 {
		t.miss("no final queue drain observation")
	}
	if w.ManagedTL1 {
		if t.initial.History.ID == "" || t.changed.History.ID == "" || t.fresh.History.ID == "" {
			t.miss("managed original/variant/refire proof")
		}
		for _, key := range []string{"native.tl1.started", "native.tl1.closed", "native.managed.functional", "native.action.children", "native.action.status", "native.managed.refresh", "native.managed.effects", "native.bulk.isolation", "native.resolved.eligibility", "native.acknowledged.repeat", "timer/resend/major", "timer/escalate/critical", "timer/resend/critical", "ticket/" + managed.AID(r.id, "AUTO", 0), "ticket/" + managed.AID(r.id, "MANUAL", 0)} {
			if t.Facets[key] == 0 {
				t.miss(key)
			}
		}
		if r.s.ID == "alerting-day" {
			for _, key := range []string{"native.synthetic_ticket", "native.policy.changes", "native.suppression_changed/silence", "native.suppression_changed/inhibit", "native.suppression_released/silence", "native.suppression_released/inhibit"} {
				if t.Facets[key] != 1 {
					t.miss(key)
				}
			}
		}
		for k, n := range t.intents {
			if n != t.writes[k] {
				t.miss("TL1 intent lacks matching wire disposition")
			}
		}
		for k, n := range t.writes {
			if n != t.intents[k] {
				t.miss("unmatched wire disposition")
			}
		}
		for cohort, c := range t.Wire {
			if c.Unknown+c.NotEntered > 0 || c.Offered != c.Written {
				t.miss(cohort + " incomplete transport")
			}
		}
	}
	if w.ManagedSegment > 0 {
		if t.Facets["native.managed.drain"] != 1 {
			t.miss("missing final managed source/provider reconciliation")
		}
		for i := 0; time.Duration(i)*w.ManagedSegment < r.s.LoadEnd; i++ {
			for _, lane := range []string{"POL", "REPEAT"} {
				for _, kind := range []string{"native.managed.segment", "native.managed.segment_closed"} {
					key := fmt.Sprintf("%s/%s/%d", kind, lane, i)
					if !t.segments[key] {
						t.miss(key)
					}
				}
				for _, code := range []string{"MJ", "CL"} {
					if t.writes[[3]string{managed.AID(r.id, "standing-"+lane, i), managed.AID(r.id, lane, 0), code}] != 1 {
						t.miss("missing sentinel input")
					}
				}
			}
		}
		for i := 0; ; i++ {
			offer, ok := managed.MixedOffer(i, w.ManagedRate, r.s.LoadEnd)
			if !ok {
				break
			}
			t.PlannedRatePDUs++
			scope := "POL"
			if offer.Slot%2 == 0 {
				scope = "PLAIN"
			}
			if t.writes[[3]string{managed.AID(r.id, "mixed", offer.Slot), managed.AID(r.id, scope, 0), offer.Code}] != 1 {
				t.miss("mixed scheduled input missing or duplicated")
			}
			if !t.slots[offer.Slot] {
				t.miss("mixed rate slot lacks downstream proof")
			}
		}
		t.RateSlots = len(t.slots)
		t.RequestedShare = w.ManagedShare
		t.PlannedBaselineInLoad = lane.BaselineOffers(r.s)
		c := t.wire("mixed_rate")
		if c.DuringLoad != t.PlannedRatePDUs || c.Offered != t.PlannedRatePDUs || t.RateSlots != t.PlannedRatePDUs/3 {
			t.miss("mixed rate planned/loaded/observed counts differ")
		}
		t.ConfiguredShare = float64(t.PlannedRatePDUs) / float64(t.PlannedRatePDUs+t.PlannedBaselineInLoad)
		if w.ManagedShare == 0 {
			t.RequestedShare = w.ManagedRate * r.s.LoadEnd.Seconds() / (w.ManagedRate*r.s.LoadEnd.Seconds() + float64(t.PlannedBaselineInLoad))
		}
		if n := c.DuringLoad + t.AcceptedBaselineInLoad; n > 0 {
			t.AchievedShare = float64(c.DuringLoad) / float64(n)
		}
		t.WrittenRatePDUsPerSecond = float64(c.DuringLoad) / r.s.LoadEnd.Seconds()
	}
	tl1 := 0
	for k, v := range t.SourceOffers {
		t.TotalOfferedInLoad += v.InLoad
		if strings.HasPrefix(k, "tl1_") {
			tl1 += v.InLoad
		}
		if v.Unknown > 0 {
			t.miss(k + " unconfirmed input")
		}
	}
	if t.TotalOfferedInLoad > 0 {
		t.OverallTL1OfferedShare = float64(tl1) / float64(t.TotalOfferedInLoad)
	}
	t.OverallOfferedRate = float64(t.TotalOfferedInLoad) / r.s.LoadEnd.Seconds()
	t.PhysicalRequests = len(t.physical)
	t.ProviderMemberReceipts = len(t.members)
}
func (r *run) nativeScore(ctx context.Context, card *score.Scorecard, in score.Inputs, end time.Time, report map[string]any) {
	t := newNativeTally(r)
	for k, v := range r.sourceTotals {
		t.SourceOffers[k] = v
	}
	after := 0
	for {
		page, err := r.store().CampaignEventsAfter(ctx, r.id, after, 128)
		if err != nil {
			t.miss("native event ledger unavailable")
			break
		}
		for _, ev := range page {
			t.consume(ev)
			after = ev.Sequence
		}
		if len(page) < 128 {
			break
		}
	}
	if !r.managedOnly() {
		r.nativeLoad(ctx, t, card, in, end, report)
	}
	t.finish()
	for _, cohort := range []string{"functional", "segment_POL", "segment_REPEAT"} {
		for _, stage := range []string{"dispatch_lag", "provider_entry", "provider_confirmation"} {
			name := cohort + "." + stage
			if vs := t.timing[name]; len(vs) > 0 {
				card.Headlines = append(card.Headlines, percentiles("native."+name, vs)...)
			} else if r.s.NativeWorkload.ManagedTL1 && (cohort == "functional" || r.s.NativeWorkload.ManagedSegment > 0) {
				t.miss("missing measured " + name)
			}
		}
	}
	judge(card, t.Failures, t.Missing)
	report["nativeQualification"] = t.nativeEvidence
}

func (t *nativeTally) timed(cohort string, due time.Time, w dispatchWindow, e managed.Effect) {
	entered, err := time.Parse(time.RFC3339Nano, e.EnteredAt)
	if err != nil {
		entered, err = time.Parse(time.RFC3339Nano, e.EnteredAt+"Z")
	}
	confirmed, er := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
	if err != nil || er != nil || w.By.IsZero() || confirmed.Before(entered) || entered.Before(due) || w.By.Before(due) {
		t.miss("invalid timer/provider timestamps for " + cohort)
		return
	}
	for k, v := range map[string]time.Duration{"dispatch_lag": w.By.Sub(due), "provider_entry": entered.Sub(due), "provider_confirmation": confirmed.Sub(entered)} {
		key := cohort + "." + k
		t.timing[key] = append(t.timing[key], v.Seconds())
	}
}
