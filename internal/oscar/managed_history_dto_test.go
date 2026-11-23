package oscar

import (
	"encoding/json"
	"testing"
)

func TestManagedHistoryCurrentEpisodeProjection(t *testing.T) {
	var raw historyWire
	if err := json.Unmarshal([]byte(`{"ID":"original","source_severity":"major","effective_severity":"critical","severity":"critical","episode_number":2,"occurrence_count":3,"acknowledged":true,"ticket_id":"INC1","labels":[{"Label":"aid","Value":"owned"}],"annotations":[{"Annotation":"episode_id","Value":"wire"}]}`), &raw); err != nil {
		t.Fatal(err)
	}
	h := raw.normalized()
	if h.ID != "original" || h.SourceSeverity != "major" || h.EffectiveSeverity != "critical" || h.EpisodeNumber != 2 || h.OccurrenceCount != 3 || !h.Acknowledged || h.TicketID != "INC1" || h.Labels["aid"] != "owned" || h.Annotations["episode_id"] != "wire" {
		t.Fatal(h)
	}
}
