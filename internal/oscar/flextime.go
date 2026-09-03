package oscar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// flexTime decodes RFC 3339 timestamps and the zone-less variant that OSCAR's
// external API emits (for example "2026-08-27T17:10:36.326000"). Zone-less
// values are interpreted as UTC. Marshalling always produces RFC 3339.
type flexTime struct {
	time.Time
}

var flexTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

func (t *flexTime) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	for _, layout := range flexTimeLayouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			t.Time = parsed.UTC()
			return nil
		}
	}
	return fmt.Errorf("unsupported timestamp %q", raw)
}

func (t flexTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Time)
}
