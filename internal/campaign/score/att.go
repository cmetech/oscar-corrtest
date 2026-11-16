package score

import (
	"fmt"
	"sort"
)

// ATT is the AT&T-shape section of a v2 scorecard: the 26.4.4 failure modes
// (2026-11-16 capacity findings) measured on this run. It is report-only in v2;
// gates follow once v2 has reference runs (ruling C23: limits are set before a
// run and never relaxed to pass).
type ATT struct {
	Standing StandingStats      `json:"standing"`
	Breaker  BreakerStats       `json:"breaker"`
	Budgets  map[string]float64 `json:"budgets"`
	Findings []string           `json:"findings"`
}

// StandingStats counts standing-pool posts by outcome: ok, rejected_503,
// error (any other HTTP failure) and unknown (no answer).
type StandingStats struct {
	Members  int            `json:"members"`
	Posts    map[string]int `json:"posts"`
	Resolves map[string]int `json:"resolves"`
}

// BreakerStats is the forced breaker window. ReopenAfterSeconds is -1 when a
// firing post was never admitted again inside the window.
type BreakerStats struct {
	Applied            bool           `json:"applied"`
	Skipped            string         `json:"skipped,omitempty"`
	Tripped            bool           `json:"tripped"`
	TripAfterSeconds   float64        `json:"tripAfterSeconds"`
	Burst              map[string]int `json:"burst"`
	ResolveProbes      int            `json:"resolveProbes"`
	ResolveAdmitted    int            `json:"resolveAdmitted"`
	ReopenAfterSeconds float64        `json:"reopenAfterSeconds"`
	Restored           bool           `json:"restored"`
}

// Outcome buckets an HTTP status the way the standing pool counts it.
func Outcome(status int) string {
	switch {
	case status == 0:
		return "unknown"
	case status/100 == 2:
		return "ok"
	case status == 503:
		return "rejected_503"
	}
	return "error"
}

// BudgetQueries (query set q2) are per-alert cost inputs over the steady
// window [T0, breaker start]; BreakerQueries cover the breaker window. Each
// takes the window length (a PromQL duration) for %s and is evaluated at the
// window end. Names confirmed against the live VictoriaMetrics on 2026-11-16.
var BudgetQueries = map[string]string{
	"alerts":             `sum(increase(celery_task_sent_total{name="tm.alerts.send_individual_notification"}[%s]))`,
	"notifier_jobs":      `sum(increase(celery_task_sent_total{name="tm.notifier.common"}[%s]))`,
	"handoff_publishes":  `sum(increase(celery_task_sent_total{name="tm.notifier.ordinary_history_handoff"}[%s]))`,
	"handoffs_sent":      `sum(increase(oscar_history_gateway_handoffs_total{outcome="sent"}[%s]))`,
	"recover_deferred":   `sum(increase(oscar_history_gateway_handoffs_total{outcome="recover_deferred"}[%s]))`,
	"history_creates":    `sum(increase(oscar_alert_history_commits_total{operation="create"}[%s]))`,
	"history_updates":    `sum(increase(oscar_alert_history_commits_total{operation="update"}[%s]))`,
	"mysql_queries":      `sum(increase(mysql_global_status_queries[%s]))`,
	"redis_growth_bytes": `sum(delta(redis_memory_used_bytes[%s]))`,
}

var BreakerQueries = map[string]string{
	"am_webhook_sent":       `sum(increase(alertmanager_notifications_total{integration="webhook"}[%s]))`,
	"am_webhook_failed":     `sum(increase(alertmanager_notifications_failed_total{integration="webhook"}[%s]))`,
	"ingest_rejected_queue": `sum(increase(oscar_alert_ingest_rejected_total{reason="queue"}[%s]))`,
	"breaker_state_max":     `max(max_over_time(oscar_alert_circuit_breaker_state[%s]))`,
}

// Derive adds the per-alert ratios to Budgets and states, in plain words, each
// AT&T failure mode the run reproduced or could not exercise.
func (a *ATT) Derive() {
	b := a.Budgets
	if b == nil {
		b = map[string]float64{}
		a.Budgets = b
	}
	ratio := func(key, num, den string) (float64, bool) {
		n, okN := b[num]
		d, okD := b[den]
		if !okN || !okD || d <= 0 {
			return 0, false
		}
		b[key] = n / d
		return b[key], true
	}
	var f []string
	if v, ok := ratio("notifier_jobs_per_alert", "notifier_jobs", "alerts"); ok {
		f = append(f, fmt.Sprintf("notifier jobs per alert: %.2f (one per enabled target, AT&T 3)", v))
	}
	if v, ok := ratio("handoff_copies", "handoff_publishes", "handoffs_sent"); ok && v > 1.2 {
		f = append(f, fmt.Sprintf("hand-off re-queue: %.2f publishes per hand-off (AT&T 10.7)", v))
	}
	ratio("mysql_queries_per_alert", "mysql_queries", "alerts")
	if u, c := b["history_updates"], b["history_creates"]; u+c > 0 {
		b["repeat_share"] = u / (u + c)
	}
	if bytes, ok := b["redis_growth_bytes"]; ok && bytes > 256<<20 {
		f = append(f, fmt.Sprintf("Redis grew %.0f MiB over the steady window", bytes/(1<<20)))
	}
	br := a.Breaker
	switch {
	case !br.Applied:
		f = append(f, "breaker window not run: "+br.Skipped)
	case !br.Tripped:
		f = append(f, "breaker did not trip: the burst never pushed the queues past the forced threshold")
	default:
		if br.ResolveAdmitted < br.ResolveProbes {
			f = append(f, fmt.Sprintf("breaker rejected %d of %d resolve-only posts", br.ResolveProbes-br.ResolveAdmitted, br.ResolveProbes))
		}
		if br.ReopenAfterSeconds < 0 {
			f = append(f, "breaker did not reopen inside the window")
		}
	}
	if v := b["am_webhook_failed"]; v > 0 {
		f = append(f, fmt.Sprintf("Alertmanager webhook failures in the breaker window: %.0f of %.0f sends (retry storm)", v, b["am_webhook_sent"]))
	}
	if br.Applied && !br.Restored {
		f = append(f, "queue threshold override NOT restored")
	}
	for _, k := range []string{"error", "unknown"} {
		if n := a.Standing.Posts[k]; n > 0 {
			f = append(f, fmt.Sprintf("standing pool: %d posts %s", n, k))
		}
	}
	sort.Strings(f)
	a.Findings = f
}
