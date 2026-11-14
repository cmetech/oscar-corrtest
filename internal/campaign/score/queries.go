// Package score reconciles a campaign's ledgers and evidence into the fixed
// scorecard, verdict and comparison (spec sections 9.2 and 9.5).
package score

// QuerySetVersion names the frozen PromQL set below; it feeds the scenario
// hash through scenario.Limits.QuerySetVersion. Changing any query means q2.
const QuerySetVersion = "q1"

// Queries is the frozen q1 PromQL set. Metric names were confirmed against the
// live VictoriaMetrics (/ext/vmdb) on 2026-11-14.
var Queries = map[string]string{
	"parent_anon_bytes":    `process_resident_memory_anon_bytes`,
	"container_workingset": `container_memory_working_set_bytes{name=~"oscar.*"}`,
	"container_cpu":        `rate(container_cpu_usage_seconds_total{name=~"oscar.*"}[1m])`,
	"queue_depth":          `oscar_celery_queue_waiting_messages`,
	"queue_oldest_age":     `oscar_celery_queue_oldest_age_seconds`,
	"mysql_threads":        `mysql_global_status_threads_connected`,
	"redis_used_bytes":     `redis_memory_used_bytes`,
	"node_mem_available":   `node_memory_MemAvailable_bytes`,
	// Guard inputs. The brief's mysql_global_status_connection_errors_max_connections
	// does not exist; the exporter exposes it as an error label of the _total series.
	"mysql_max_conn_errors": `mysql_global_status_connection_errors_total{error="max_connections"}`,
	"redis_rejected_cmds":   `sum(redis_commands_rejected_calls_total)`,
}
