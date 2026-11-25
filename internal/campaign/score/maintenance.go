package score

import (
	"fmt"
	"strings"
	"time"
)

// Maintenance-mode lane (OSC-189). A window suppresses the alerthistory
// handler for alerts matching its pattern: OSCAR still writes the History row,
// with status "suppressed" and annotation suppression_window=<cr>, and skips
// ticketing. Suppression is decided when the handler processes the delivery,
// so a probe is judged by its row's createdAt (server insert time, after the
// rule evaluation), not by when CorrTest posted it.

// MaintProbe kinds.
const (
	MaintInside  = "inside"  // matching, posted inside the window (create_ticket=snow)
	MaintControl = "control" // not matching, posted inside the window
	MaintAfter   = "after"   // matching, posted after the window
)

// MaintProbe is one probe alert and what History holds for it.
type MaintProbe struct {
	Kind, Name string
	PostedAt   time.Time
	Found      bool
	Status     string    `json:",omitempty"`
	Window     string    `json:",omitempty"` // its suppression_window annotation
	CreatedAt  time.Time `json:",omitzero"`
	Ticketed   bool      `json:",omitempty"`
}

// MaintEdge is one status read of the window: what it should and did read,
// and whether the read came too late to judge.
type MaintEdge struct {
	Want, Got string
	At        time.Time
	Late      bool   `json:",omitempty"`
	Bounds    string `json:",omitempty"` // the start/end OSCAR reported, when they differ from the request
}

// MaintenanceStats is the scorecard's Maintenance section.
type MaintenanceStats struct {
	Skipped    string `json:",omitempty"`
	CR         string `json:",omitempty"`
	Start, End time.Time
	// Setup is why the lane could not run (a failed create or enable).
	Setup string `json:",omitempty"`
	// Applied is what OSCAR applied when it differs from our pattern and
	// handler (an always-applied pattern joined): the lane disabled at once.
	Applied      string `json:",omitempty"`
	Edges        []MaintEdge
	Probes       []MaintProbe
	Posted       int  // probe posts, outside the offered-rate budget
	SinkReceipts int  // inside probes received by the sink (report-only)
	Late         int  // inside probes processed after the window end (not judged)
	Removed      bool // window, template and pattern removed and read back by the lane (cleanup otherwise)
}

// Judge returns the lane's FAIL and INCONCLUSIVE reasons; every gate is 100%.
func (s MaintenanceStats) Judge() (fails, incon []string) {
	if s.Skipped != "" {
		return nil, nil
	}
	if s.Applied != "" {
		return []string{"maintenance: the window applied " + s.Applied + ", not only the campaign pattern and alerthistory; disabled"}, nil
	}
	if s.Setup != "" {
		return nil, []string{"maintenance: lane did not run: " + s.Setup}
	}
	for _, e := range s.Edges {
		switch {
		case e.Late:
			incon = append(incon, fmt.Sprintf("maintenance: %s read too late to judge (%s)", e.Want, e.At.UTC().Format(time.RFC3339)))
		case e.Got != e.Want:
			fails = append(fails, fmt.Sprintf("maintenance: window read %q at %s, want %s", e.Got, e.At.UTC().Format(time.RFC3339), e.Want))
		case e.Bounds != "":
			fails = append(fails, "maintenance: window bounds "+e.Bounds+", not the requested ones")
		}
	}
	judged, late := 0, 0
	var missing, bad, ticketed, foreign []string
	for _, p := range s.Probes {
		if !p.Found {
			missing = append(missing, p.Name)
			continue
		}
		marked := strings.EqualFold(p.Status, "suppressed") && p.Window == s.CR
		switch p.Kind {
		case MaintInside:
			if p.CreatedAt.After(s.End) { // processed after the window: OSCAR rightly did not suppress it
				late++
				continue
			}
			judged++
			if !marked {
				bad = append(bad, fmt.Sprintf("%s inside the window read %s/%q", p.Name, p.Status, p.Window))
			}
			if p.Ticketed {
				ticketed = append(ticketed, p.Name)
			}
		default:
			switch {
			case p.Window == s.CR || strings.EqualFold(p.Status, "suppressed") && p.Window == "":
				bad = append(bad, fmt.Sprintf("%s (%s) read %s/%q", p.Name, p.Kind, p.Status, p.Window))
			case p.Window != "":
				foreign = append(foreign, fmt.Sprintf("%s suppressed by window %q", p.Name, p.Window))
			}
		}
	}
	if len(bad) > 0 {
		fails = append(fails, "maintenance: "+strings.Join(bad, "; "))
	}
	if len(ticketed) > 0 {
		fails = append(fails, "maintenance: ticketed inside the window: "+strings.Join(ticketed, ", "))
	}
	if len(missing) > 0 {
		incon = append(incon, fmt.Sprintf("maintenance: %d probes without a History row: %s", len(missing), strings.Join(missing, ", ")))
	}
	if len(foreign) > 0 {
		incon = append(incon, "maintenance: "+strings.Join(foreign, "; "))
	}
	if judged == 0 {
		incon = append(incon, fmt.Sprintf("maintenance: no inside probe processed inside the window (%d after its end)", late))
	}
	return fails, incon
}
