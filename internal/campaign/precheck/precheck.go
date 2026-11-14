// Package precheck decides whether a campaign may start: 11 blocking checks (B1..B11) and 5 advisory checks (A1..A5).
package precheck

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type Tier string

const (
	Blocking Tier = "blocking"
	Advisory Tier = "advisory"
)

// Result statuses.
const (
	Pass  = "pass"
	Fail  = "fail"
	Warn  = "warn"
	Error = "error" // the check could not run; blocks like a failure
)

type Result struct {
	ID       string // B1..B11, A1..A5
	Tier     Tier
	Status   string // pass|fail|warn|error
	Measured string
	Detail   string
}

// Timing brackets one request on the controller clock; the server clock is compared with the midpoint.
type Timing struct{ Sent, Recv time.Time }

func (t Timing) Mid() time.Time { return t.Sent.Add(t.Recv.Sub(t.Sent) / 2) }

type Queue struct {
	Name      string
	Depth     int
	OldestAge time.Duration
}

// SNOWRate is OSCAR's ticketing rate-limit view (GET /api/v1/alerts/ratelimits/ticketing), snow provider only.
type SNOWRate struct {
	Healthy, Configured, Paused bool
	OpsPerSecond                float64
}

type FOUser struct{ Name, Key string }

// Deps is one method per data source so tests use a fake. The engine adapts the targetops clients, persistence and
// the worker client to it.
type Deps interface {
	Now() time.Time
	CampaignID() string
	ControllerVersion() string // CorrTest version the worker must match
	Sleep(ctx context.Context, d time.Duration) error

	Probe(ctx context.Context) (targetops.ProbeResult, Timing, error)
	WorkerInfo(ctx context.Context) (workerapi.Info, Timing, error)
	ResendSchema(ctx context.Context) error // targetops resend Status; nil = schema present
	HistoryNotifierCount(ctx context.Context) (int, error)
	Queues(ctx context.Context) ([]Queue, error)
	CampaignRules(ctx context.Context) ([]string, error) // enabled correlation rules matching campaign_* other than this run's own

	ListCampaigns(ctx context.Context, f domain.CampaignFilter) ([]domain.Campaign, error)
	OpenMutations(ctx context.Context, campaignID string) ([]domain.CampaignMutation, error)
	RecordMutation(ctx context.Context, m domain.CampaignMutation) (int64, error)
	MarkMutationRestored(ctx context.Context, id int64, at time.Time) error

	SinkHealth(ctx context.Context) error
	SinkEnabled(ctx context.Context) (bool, error)
	SetSinkEnabled(ctx context.Context, on bool) error // enable/disable CAMPAIGN_SINK with readback
	PostAlert(ctx context.Context, labels map[string]string) error
	SinkReceipts(ctx context.Context, campaignRun string, since time.Time) ([]targetops.Receipt, error)
	ExternalNotifiers(ctx context.Context) ([]targetops.Notifier, error) // enabled, excluding CAMPAIGN_SINK and Alert History
	OpenCampaignAlerts(ctx context.Context) (int, error)
	LeftoverObjects(ctx context.Context) ([]string, error) // campaign rules or policies still on the target

	ScrapeDown(ctx context.Context) ([]string, error)
	Freshness(ctx context.Context) (map[string]time.Duration, error) // newest-sample age per scorecard family

	SNOWRate(ctx context.Context) (SNOWRate, error)
	SNOWWake(ctx context.Context) error
	SNOWCredential(ctx context.Context) error
	OpenIncidents(ctx context.Context, campaignRun string) ([]targetops.Incident, error)

	TaskRuns(ctx context.Context, jobs []string, start, end time.Time) ([]targetops.TaskRun, error)
	FrontOfficeUsers() ([]FOUser, error)
	FOHistory(ctx context.Context, apiKey string) (int, error) // HTTP status of GET /api/v1/alerts/history?perPage=1

	LocalDiskFree() (uint64, error)
	TargetHeadroom(ctx context.Context) (diskFreePct, memAvailPct float64, err error)

	HostsDown(ctx context.Context) ([]string, error)
	FiringBacklog(ctx context.Context) (count, idleBaseline int, err error)
	ForeignArrivalRate(ctx context.Context, window time.Duration) (perSecond float64, err error)
}

// Run executes every check in order. A failure never stops later checks, so the report is complete.
func Run(ctx context.Context, d Deps, s scenario.Scenario, minVersion string) []Result {
	var probe *probeData
	getProbe := func() *probeData {
		if probe == nil {
			probe = &probeData{}
			probe.r, probe.t, probe.err = d.Probe(ctx)
		}
		return probe
	}
	steps := []func() Result{
		func() Result { return b1(ctx, d, getProbe(), minVersion) },
		func() Result { return b2(d, getProbe(), s) },
		func() Result { return b3(ctx, d, s) },
		func() Result { return b4(ctx, d, getProbe(), s) },
		func() Result { return b5(ctx, d) },
		func() Result { return b6(ctx, d, s) },
		func() Result { return b7(ctx, d) },
		func() Result { return b8(ctx, d) },
		func() Result { return b9(ctx, d) },
		func() Result { return b10(ctx, d) },
		func() Result { return b11(ctx, d, getProbe(), s) },
		func() Result { return a1(ctx, d) },
		func() Result { return a2(getProbe()) },
		func() Result { return a3(ctx, d) },
		func() Result { return a4(ctx, d) },
		func() Result { return a5(getProbe()) },
	}
	out := make([]Result, 0, len(steps))
	for _, st := range steps {
		out = append(out, st())
	}
	return out
}

// Startable reports whether the campaign may start and whether the run stays comparable. Any blocking check that is
// not a pass stops it, acknowledged or not. Each non-passing advisory needs its ID in acknowledged; acknowledging one
// marks the run not comparable.
func Startable(results []Result, acknowledged []string) (ok bool, comparable bool, reason string) {
	ack := map[string]bool{}
	for _, a := range acknowledged {
		ack[a] = true
	}
	var blocked, unacked []string
	comparable = true
	for _, r := range results {
		if r.Status == Pass {
			continue
		}
		if r.Tier == Blocking {
			blocked = append(blocked, r.ID)
		} else if ack[r.ID] {
			comparable = false
		} else {
			unacked = append(unacked, r.ID)
		}
	}
	sort.Strings(blocked)
	sort.Strings(unacked)
	switch {
	case len(blocked) > 0:
		return false, false, "blocking checks not passing: " + strings.Join(blocked, ", ")
	case len(unacked) > 0:
		return false, false, "advisory checks need acknowledgment: " + strings.Join(unacked, ", ")
	case !comparable:
		return true, false, "advisories acknowledged; run is not comparable"
	}
	return true, true, ""
}

func res(id string, tier Tier, ok bool, measured string, problems []string) Result {
	r := Result{ID: id, Tier: tier, Measured: measured, Detail: strings.Join(problems, "; ")}
	switch {
	case len(problems) == 0 && ok:
		r.Status = Pass
	case tier == Advisory:
		r.Status = Warn
	default:
		r.Status = Fail
	}
	return r
}

func errRes(id string, tier Tier, what string, err error) Result {
	return Result{ID: id, Tier: tier, Status: Error, Detail: fmt.Sprintf("%s: %v", what, err)}
}
