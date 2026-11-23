package engine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Routes carry the effective native settings and two lossless source cursor
// bookmarks for the run-owned outbound population. A missing attempt must
// create a sequence gap, even if the destination never accepted the request.
type captureRoute struct {
	Receiver, Recipient                            string
	GroupWaitNS, GroupIntervalNS, RepeatIntervalNS int64
	Before, Through                                int64
	Dropped                                        *int
}
type capturedMember struct {
	Name, Status string
	Start        time.Time
}
type routeDecision struct {
	ID, Group, GroupState, Route, Kind string
	Opened, At, NativeConfirmed        time.Time
	Members                            []capturedMember
}
type capturedRequest struct {
	RequestID, DecisionID, Origin, Receiver, Recipient, Outcome string
	Sequence                                                    int64
	Started, Finished                                           time.Time
	ReceiptID                                                   int64
	Members                                                     []capturedMember
}

func configurationDigest(observer string, routes map[string]captureRoute) string {
	settings := map[string]captureRoute{}
	for name, r := range routes {
		r.Before, r.Through, r.Dropped = 0, 0, nil
		settings[name] = r
	}
	return fmt.Sprintf("%x", sha256.Sum256(mustJSON([]any{observer, settings})))
}
func sameMembers(a, b []capturedMember) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	members := map[string]capturedMember{}
	for _, m := range a {
		if m.Name == "" || members[m.Name].Name != "" {
			return false
		}
		members[m.Name] = m
	}
	for _, m := range b {
		v, ok := members[m.Name]
		if !ok || v.Status != m.Status || !v.Start.Equal(m.Start) {
			return false
		}
		delete(members, m.Name)
	}
	return true
}

type observedContact struct {
	decision  routeDecision
	confirmed time.Time
}

func (r *run) ordinaryContacts(t *nativeTally, c costFile, rs []targetops.Receipt) (map[string]map[string]bool, error) {
	if len(c.Routes) != 2 || len(c.Decisions) == 0 || len(c.Requests) == 0 {
		return nil, errors.New("independent receiver decisions and outbound request inventory required")
	}
	for _, name := range []string{"direct", "canonical"} {
		route := c.Routes[name]
		if route.Receiver == "" || route.Recipient == "" || route.GroupWaitNS < 0 || route.GroupWaitNS >= int64(5*time.Minute) || route.GroupIntervalNS <= 0 || route.GroupIntervalNS >= int64(5*time.Minute) || route.RepeatIntervalNS <= 0 || route.RepeatIntervalNS >= int64(5*time.Minute) || route.Dropped == nil || *route.Dropped != 0 || route.Before < 0 || route.Through <= route.Before {
			return nil, errors.New("effective native route settings or source cursor unavailable")
		}
		if name == "canonical" && route.Recipient != r.e.d.CampaignNotifier || name == "direct" && route.Recipient != route.Receiver {
			return nil, errors.New("uncommissioned ordinary recipient")
		}
	}
	decisions := map[string]*observedContact{}
	groups := map[[2]string][]*observedContact{}
	for _, d := range c.Decisions {
		route, ok := c.Routes[d.Route]
		if !ok || d.NativeConfirmed.After(c.Through) || !digest(d.ID, 32) || !digest(d.Group, 32) || !digest(d.GroupState, 32) || decisions[d.ID] != nil || d.At.Before(c.From) || d.At.After(c.Through) || !sameMembers(d.Members, d.Members) {
			return nil, errors.New("invalid or duplicate native group decision")
		}
		for _, m := range d.Members {
			src, ok := t.ordinary[m.Name]
			if !ok || len(src.Receivers) != 1 || src.Receivers[0].Name != route.Receiver || src.Labels["campaign_lane"] != "native-"+d.Route || !src.StartsAt.Equal(m.Start) || d.At.Before(m.Start) || m.Status != "firing" && m.Status != "resolved" {
				return nil, errors.New("decision missing owned member/start/receiver")
			}
		}
		contact := &observedContact{decision: d}
		decisions[d.ID] = contact
		key := [2]string{d.Route, d.Group}
		groups[key] = append(groups[key], contact)
	}
	receipts := map[int64]targetops.Receipt{}
	for _, rc := range rs {
		var owned []targetops.SinkAlert
		for _, a := range rc.Alerts {
			if _, ok := t.ordinary[a.AlertName]; ok {
				owned = append(owned, a)
			}
		}
		if len(owned) > 0 {
			if rc.ID < 1 || receipts[rc.ID].ID != 0 {
				return nil, errors.New("ambiguous ordinary capture receipt")
			}
			rc.Alerts = owned
			receipts[rc.ID] = rc
		}
	}
	sequences := map[string]map[int64]bool{"direct": {}, "canonical": {}}
	cohorts := map[string]map[string]bool{}
	for _, q := range c.Requests {
		contact := decisions[q.DecisionID]
		if contact == nil || !digest(q.RequestID, 32) || cohorts[q.RequestID] != nil {
			return nil, errors.New("outbound request lacks a unique original request and decision")
		}
		d := contact.decision
		route := c.Routes[d.Route]
		if q.Sequence <= route.Before || q.Sequence > route.Through || sequences[d.Route][q.Sequence] || q.Receiver != route.Receiver || q.Recipient != route.Recipient || q.Started.Before(d.At) || q.Finished.Before(q.Started) || q.Finished.After(c.Through) || !sameMembers(d.Members, q.Members) {
			return nil, errors.New("outbound identity/window differs from original decision")
		}
		if d.Route == "direct" && q.Origin != "native_webhook" || d.Route == "canonical" && q.Origin != "oscar_recipient" {
			return nil, errors.New("unknown outbound origin for ordinary route")
		}
		sequences[d.Route][q.Sequence] = true
		switch q.Outcome {
		case "accepted":
			if !contact.confirmed.IsZero() {
				t.fail("duplicate successful request on original native decision")
			}
			contact.confirmed = q.Finished
		case "rejected":
			t.fail("ordinary outbound request definitively failed")
		case "unknown":
			t.miss("ordinary outbound request outcome uncertain")
		default:
			return nil, errors.New("outbound outcome missing")
		}
		if q.ReceiptID != 0 {
			rc, ok := receipts[q.ReceiptID]
			if !ok || rc.ReceivedAt.Before(q.Started) || rc.ReceivedAt.After(q.Finished) || d.Route == "direct" && rc.Receiver != q.Receiver || d.Route == "canonical" && rc.Notifier != q.Recipient {
				return nil, errors.New("outbound request differs from independent sink recipient/interval")
			}
			var members []capturedMember
			for _, a := range rc.Alerts {
				start, err := time.Parse(time.RFC3339Nano, a.StartsAt)
				if err != nil {
					return nil, errors.New("missing sink member start")
				}
				members = append(members, capturedMember{a.AlertName, a.Status, start})
			}
			if !sameMembers(q.Members, members) {
				return nil, errors.New("outbound and sink member sets differ")
			}
			delete(receipts, q.ReceiptID)
		} else if q.Outcome == "accepted" {
			t.miss("accepted ordinary outbound request lacks independent capture")
		}
		cohorts[q.RequestID] = map[string]bool{}
		for _, m := range q.Members {
			if m.Status != "firing" {
				continue
			}
			cohort := "direct"
			if d.Route == "canonical" {
				cohort = "canonical_first"
				if d.Kind == "repeat" {
					cohort = "canonical_repeat"
				} else if d.Kind == "update" {
					cohort = "canonical_update"
				}
			}
			cohorts[q.RequestID][cohort] = true
		}
	}
	if len(receipts) != 0 {
		return nil, errors.New("sink receipts outside outbound population")
	}
	for route, seqs := range sequences {
		spec := c.Routes[route]
		if int64(len(seqs)) != spec.Through-spec.Before {
			return nil, errors.New("gap in outbound attempt population")
		}
	}
	r.checkContactSchedule(t, c.Routes, groups)
	return cohorts, nil
}
func repeatWindow(last time.Time, route captureRoute) (time.Time, time.Time) {
	due := last.Add(time.Duration(route.RepeatIntervalNS))
	return due, due.Add(time.Duration(route.GroupIntervalNS) + 2*time.Second)
}
func (r *run) checkContactSchedule(t *nativeTally, routes map[string]captureRoute, groups map[[2]string][]*observedContact) {
	seen, repeat, resolved := map[string]bool{}, map[string]bool{}, map[string]bool{}
	membership := map[string][2]string{}
	for key, contacts := range groups {
		route := routes[key[0]]
		sort.Slice(contacts, func(i, j int) bool { return contacts[i].decision.At.Before(contacts[j].decision.At) })
		var previous *observedContact
		for _, current := range contacts {
			d := current.decision
			if d.NativeConfirmed.Before(d.At) {
				t.miss("native group confirmation timestamp unavailable")
				continue
			}
			if current.confirmed.IsZero() {
				t.miss("native group decision lacks successful physical confirmation")
				continue
			}
			newGroup := previous == nil
			if previous != nil && d.Kind == "initial" && d.Opened.After(previous.decision.NativeConfirmed) {
				newGroup = true
				for _, m := range previous.decision.Members {
					if m.Status != "resolved" {
						newGroup = false
					}
				}
			}
			if newGroup {
				due := d.Opened.Add(time.Duration(route.GroupWaitNS))
				if d.Opened.Before(r.t0) || d.At.Before(due) || d.At.After(due.Add(2*time.Second)) {
					t.miss("original native group wait/flush timing unavailable")
				}
				if d.Kind != "initial" {
					t.miss("native group lacks original initial dispatch")
				}
			} else {
				due, latest := repeatWindow(previous.decision.NativeConfirmed, route)
				for _, m := range previous.decision.Members {
					clear := t.clearAt[m.Name]
					if m.Status == "firing" && latest.Before(clear) && d.At.After(latest) {
						t.miss("due ordinary repeat missing before next group decision")
					}
				}
				switch d.Kind {
				case "repeat":
					if d.GroupState != previous.decision.GroupState || !sameMembers(d.Members, previous.decision.Members) || d.At.Before(due) || d.At.After(latest) {
						t.fail("ordinary repeat disagrees with native group or effective deadline")
					}
				case "update", "resolve":
					if d.GroupState == previous.decision.GroupState {
						t.fail("duplicate initial or unchanged ordinary group contact")
					}
				default:
					t.fail("unexpected ordinary group decision")
				}
			}
			for _, m := range d.Members {
				if old, ok := membership[m.Name]; ok && old != key {
					t.fail("ordinary member moved across native groups")
				}
				membership[m.Name] = key
				clear := t.clearAt[m.Name]
				if clear.IsZero() {
					t.miss("ordinary episode has no accepted source clear")
				}
				if m.Status == "resolved" {
					if d.At.Before(clear) || resolved[m.Name] || !seen[m.Name] {
						t.fail("unexpected ordinary episode resolve")
					}
					resolved[m.Name] = true
				} else {
					if d.At.After(clear.Add(2 * time.Second)) {
						t.fail("firing decision after ordinary source clear")
					}
					if resolved[m.Name] {
						t.fail("firing after ordinary resolve")
					}
					seen[m.Name] = true
					if d.Kind == "repeat" {
						repeat[m.Name] = true
					}
				}
			}
			previous = current
		}
		if previous != nil {
			_, latest := repeatWindow(previous.decision.NativeConfirmed, route)
			for _, m := range previous.decision.Members {
				if m.Status == "firing" && latest.Before(t.clearAt[m.Name]) {
					t.miss("final due native repeat absent")
				}
			}
		}
	}
	for id := range t.ordinary {
		if !seen[id] || !repeat[id] || !resolved[id] {
			t.miss("ordinary episode lacks initial, attributable due repeat or resolve: " + id)
		}
	}
}
