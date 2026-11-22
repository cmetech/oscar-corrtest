package engine

import (
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Keep all HTTP DTOs outside the event store. History can contain provider
// annotations and the signed managed binding. Native/sink label maps are open.
type observedHistory struct {
	ID, Status, SourceSeverity, EffectiveSeverity, TicketID, AlarmKey string
	EpisodeNumber, OccurrenceCount                                    int
	Labels                                                            map[string]string
}

func fixtureLabels(in map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"aid", "source_id", "node", "condition", "severity", "correlation_key", "create_ticket", "integration", "alert_type", "campaign_run", "campaign_lane", "alertname"} {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	return out
}
func historyEvidence(h oscar.HistoryRecord) observedHistory {
	return observedHistory{h.ID, h.Status, h.SourceSeverity, h.EffectiveSeverity, h.TicketID, h.Annotations["alarm_key"], h.EpisodeNumber, h.OccurrenceCount, fixtureLabels(h.Labels)}
}
func memberEvidence(v lane.NativeMember) lane.NativeMember {
	v.Labels = fixtureLabels(v.Labels)
	return v
}
func receiptEvidence(rs []targetops.Receipt) []targetops.Receipt {
	out := make([]targetops.Receipt, 0, len(rs))
	for _, r := range rs {
		alerts := make([]targetops.SinkAlert, 0, len(r.Alerts))
		for _, a := range r.Alerts {
			a.Labels = fixtureLabels(a.Labels)
			alerts = append(alerts, a)
		}
		r.Alerts = alerts
		out = append(out, r)
	}
	return out
}

// This is the serialization boundary for every managed campaign event. Incoming
// HTTP records are projected even when nested inside a new event detail map.
func managedEventEvidence(value any) any {
	switch v := value.(type) {
	case oscar.HistoryRecord:
		return historyEvidence(v)
	case []oscar.HistoryRecord:
		out := make([]observedHistory, 0, len(v))
		for _, row := range v {
			out = append(out, historyEvidence(row))
		}
		return out
	case NativeEpisodeFixture:
		v.History.Labels = fixtureLabels(v.History.Labels)
		return v
	case lane.NativeMember:
		return memberEvidence(v)
	case []targetops.Receipt:
		return receiptEvidence(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = managedEventEvidence(child)
		}
		return out
	default:
		return value
	}
}
func observedMember(v lane.NativeMember, f managed.SourceReceipt, severity string, now time.Time) bool {
	start, err := time.Parse(time.RFC3339Nano, f.Starts)
	return err == nil && v.StartsAt.Equal(start) && v.Fingerprint == f.Fingerprint && v.EndsAt.After(now) && v.Labels["severity"] == severity && v.Labels["correlation_key"] == f.Correlation && v.Labels["source_id"] == f.SourceID && v.Labels["node"] == f.TID
}

func automaticTicket(f NativeEpisodeFixture, rows []managed.TicketProof, incident string) (managed.TicketProof, error) {
	var proof managed.TicketProof
	if f.History.Labels["create_ticket"] != "snow" {
		return proof, errors.New("expected label-driven snow ticket")
	}
	for _, r := range rows {
		if r.History != f.History.ID || r.Episode != f.History.EpisodeNumber {
			continue
		}
		if proof.Flow != "" || r.Flow == "" || r.Operation == "" || r.Audit != r.Operation || r.Origin != "automatic" || r.System != "snow" || r.AuditSystem != "snow" || r.AuditStatus != "success" || r.Incident != incident || r.AuditIncident != incident {
			return proof, errors.New("ambiguous or foreign automatic provider operation")
		}
		proof = r
	}
	return proof, nil
}
