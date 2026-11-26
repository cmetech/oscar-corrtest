package score

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// ReceiverProbe is one receivers-lane probe (OSC-207) and what History
// showed for it at the lane's read-back.
type ReceiverProbe struct {
	Receiver, Name string
	Server         int
	SentAt         time.Time
	SendErr        string `json:",omitempty"`
	Found          bool
	CreatedAt      time.Time `json:",omitzero"`
	Problem        string    `json:",omitempty"` // a wrong label, Source, Source IP, severity or status
}

// ReceiverCounts are one receiver's probe outcomes.
type ReceiverCounts struct {
	Sent, SendErrors, Landed, Late, Wrong, Lost int
	LatencyP50, LatencyP95                      float64 // send to History created_at, seconds
}

// ReceiverStats is the scorecard's Receivers section.
type ReceiverStats struct {
	Skipped string `json:",omitempty"`
	Setup   string `json:",omitempty"` // mapping install failure: the lane sent nothing
	Read    string `json:",omitempty"` // History read-back failure
	Removed bool   // both mappings removed and read back absent
	// StillActive are probes native Alertmanager still held active after the
	// lane's resolve-and-confirm (OSC-210); ResolveErr its last failure.
	StillActive []string `json:",omitempty"`
	ResolveErr  string   `json:",omitempty"`
	Planned     int
	Bound       float64 // seconds
	Receivers   map[string]ReceiverCounts
	Probes      []ReceiverProbe `json:",omitempty"`
}

// ReceiverLabels are the labels a probe of server n carries (and History
// must show): the run, the lane and the server's source labels.
func ReceiverLabels(run string, n int) map[string]string {
	l := lane.ServerLabels(n)
	l["campaign_run"], l["campaign_lane"] = run, "receivers"
	return l
}

// CheckReceiverRow is "" when a History row shows the probe exactly: every
// probe label, Source and Source IP as OSCAR derives them, severity warning
// and status firing.
func CheckReceiverRow(run string, p ReceiverProbe, row oscar.HistoryRecord) string {
	var bad []string
	want := ReceiverLabels(run, p.Server)
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if row.Labels[k] != want[k] {
			bad = append(bad, fmt.Sprintf("%s=%q", k, row.Labels[k]))
		}
	}
	if got := AlertSource(row.Labels); got != lane.ServerName(p.Server) {
		bad = append(bad, fmt.Sprintf("Source %q", got))
	}
	if got := AlertSourceIP(row.Labels); got != lane.ServerIP(p.Server) {
		bad = append(bad, fmt.Sprintf("Source IP %q", got))
	}
	if !row.MatchesInputLabel("severity", "warning") {
		bad = append(bad, "severity not warning")
	}
	if !strings.EqualFold(row.Status, "firing") {
		bad = append(bad, "status "+row.Status)
	}
	return strings.Join(bad, ", ")
}

// JudgeReceivers gates the receivers lane at 100%: FAIL on a send error, a
// probe not in History at the read-back (lost), one created more than Bound
// after its send (late) or a wrong row; INCONCLUSIVE when the lane could not
// install its mappings, the read-back failed, nothing was sent against a
// positive plan, or the mappings were not removed.
func JudgeReceivers(s *ReceiverStats) (fails, incon []string) {
	s.Receivers = map[string]ReceiverCounts{}
	if s.Skipped != "" {
		return nil, nil
	}
	if s.Setup != "" {
		return nil, []string{"receivers: setup: " + s.Setup}
	}
	lat := map[string][]float64{}
	var wrong []string
	for _, p := range s.Probes {
		c := s.Receivers[p.Receiver]
		c.Sent++
		switch {
		case p.SendErr != "":
			c.SendErrors++
		case s.Read != "":
		case !p.Found:
			c.Lost++
		default:
			c.Landed++
			d := p.CreatedAt.Sub(p.SentAt).Seconds()
			lat[p.Receiver] = append(lat[p.Receiver], d)
			if d > s.Bound {
				c.Late++
			}
			if p.Problem != "" {
				c.Wrong++
				if len(wrong) < 3 {
					wrong = append(wrong, p.Name+": "+p.Problem)
				}
			}
		}
		s.Receivers[p.Receiver] = c
	}
	names := make([]string, 0, len(s.Receivers))
	for n := range s.Receivers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := s.Receivers[n]
		c.LatencyP50, c.LatencyP95 = Percentile(lat[n], 50), Percentile(lat[n], 95)
		s.Receivers[n] = c
		if c.SendErrors+c.Lost+c.Late+c.Wrong > 0 {
			fails = append(fails, fmt.Sprintf("receivers %s: %d of %d probes failed (send errors %d, lost %d, later than %.0fs %d, wrong %d)",
				n, c.SendErrors+c.Lost+c.Late+c.Wrong, c.Sent, c.SendErrors, c.Lost, s.Bound, c.Late, c.Wrong))
		}
	}
	if len(wrong) > 0 {
		fails = append(fails, "receivers wrong rows: "+strings.Join(wrong, "; "))
	}
	if s.Read != "" {
		incon = append(incon, "receivers: History read-back: "+s.Read)
	}
	if len(s.Probes) == 0 && s.Planned > 0 {
		incon = append(incon, fmt.Sprintf("receivers: no probe sent of %d planned", s.Planned))
	}
	if !s.Removed {
		incon = append(incon, "receivers: campaign mappings not removed")
	}
	if len(s.StillActive) > 0 || s.ResolveErr != "" && len(s.Probes) > 0 {
		incon = append(incon, strings.TrimSpace(fmt.Sprintf("receivers: %d probes still active in Alertmanager after the lane %s", len(s.StillActive), s.ResolveErr)))
	}
	return fails, incon
}
