package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

// Both control deployments receive identical public v2 alerts. A preprovisioned
// receiver route sends native-direct only to the test capture; native-canonical
// reaches the canonical OSCAR receiver. A refresh keeps the exact source start.
// The receiver's real repeat interval generates the callback, never the harness.
func (r *run) nativeOrdinaryPhase(ctx context.Context) {
	w := r.s.NativeWorkload
	if w == nil || len(w.Cohorts) == 0 {
		return
	}
	if r.e.d.nativeSender == nil {
		r.noteMissing("native v2 sender unavailable")
		return
	}
	for cycle := 0; ; cycle++ {
		at := r.t0.Add(time.Duration(cycle) * 5 * time.Minute)
		end := r.t0.Add(r.s.LoadEnd)
		if !at.Before(end) || !r.e.clock.SleepUntil(ctx, at) {
			return
		}
		until := at.Add(5 * time.Minute)
		if end.Before(until) {
			until = end
		}
		for _, path := range []string{"direct", "canonical"} {
			ev := lane.Event{Identity: fmt.Sprintf("Campaign-%s-native-%s-%d", r.id, path, cycle), Instance: "corrtest-native", Severity: "major", StartsAt: at.Truncate(time.Millisecond), ValidUntil: until.Add(time.Minute), Kind: lane.Firing}
			r.nativeWrite(ctx, path, ev, map[string]string{"direct": string(scenario.NativeDirectNoPolicy), "canonical": string(scenario.NativeCanonicalFirst)}[path])
			if path == "canonical" && cycle == 0 {
				r.nativeWrite(ctx, path, ev, "transport_duplicate")
			}
		}
		if r.e.clock.SleepUntil(ctx, at.Add(2*time.Minute)) && at.Add(2*time.Minute).Before(until) {
			for _, path := range []string{"direct", "canonical"} {
				ev := lane.Event{Identity: fmt.Sprintf("Campaign-%s-native-%s-%d", r.id, path, cycle), Instance: "corrtest-native", Severity: "major", StartsAt: at.Truncate(time.Millisecond), ValidUntil: until.Add(time.Minute), Kind: lane.Firing}
				r.nativeWrite(ctx, path, ev, map[string]string{"direct": string(scenario.NativeDirectNoPolicy), "canonical": string(scenario.NativeCanonicalRepeat)}[path])
			}
		}
		r.e.clock.SleepUntil(ctx, until)
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		for _, path := range []string{"direct", "canonical"} {
			ev := lane.Event{Identity: fmt.Sprintf("Campaign-%s-native-%s-%d", r.id, path, cycle), Instance: "corrtest-native", Severity: "major", StartsAt: at.Truncate(time.Millisecond), Kind: lane.Resolve}
			r.observeOrdinary(clean, ev, path)
			r.nativeWrite(clean, path, ev, "source_resolve")
		}
		stop()
		if ctx.Err() != nil {
			return
		}
	}
}
func (r *run) nativeWrite(ctx context.Context, path string, e lane.Event, cohort string) {
	started := r.now()
	status, err := r.e.d.nativeSender.Send(ctx, e, r.id, "native-"+path)
	detail := map[string]any{"source": e, "route": path, "cohort": cohort, "httpStatus": status, "offeredAt": started, "finishedAt": r.now()}
	if err != nil || status/100 != 2 {
		r.noteMissing("native input refused or outcome unknown: " + e.Identity)
	}
	r.event(context.WithoutCancel(ctx), "native.ordinary.wire", "info", "native v2 input", detail)
}

type nativeReader interface {
	Members(context.Context, map[string]string) ([]lane.NativeMember, error)
}

func (r *run) observeOrdinary(ctx context.Context, e lane.Event, path string) {
	reader, ok := r.e.d.nativeSender.(nativeReader)
	if !ok {
		r.noteMissing("native source read unavailable")
		return
	}
	scope := map[string]string{"campaign_run": r.id, "alertname": e.Identity, "campaign_lane": "native-" + path}
	members, err := reader.Members(ctx, scope)
	if err != nil || len(members) != 1 || !members[0].StartsAt.Equal(e.StartsAt) || len(members[0].Receivers) == 0 {
		r.noteMissing("native member/receiver/start unproved for " + e.Identity)
		return
	}
	r.event(ctx, "native.ordinary.observed", "info", "source read from public native v2 API", map[string]any{"identity": e.Identity, "route": path, "member": members[0]})
}
