package resend

import (
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func oracle(t *testing.T, after int) *NativeClock {
	t.Helper()
	c := &NativeClock{Target: "sink", Policy: scenario.PolicySpec{Mode: "time", Interval: 10 * time.Minute, Escalation: &scenario.EscalationSpec{After: time.Duration(after) * time.Minute, TargetSeverity: "critical"}}}
	e := NativeEvent{Kind: "new_episode", Source: SourceRef{"commissioned", "alarm", "first", epoch.Format(time.RFC3339), 1}, At: epoch, Severity: "major"}
	if _, err := c.Apply(e); err != nil {
		t.Fatal(err)
	}
	checkNative(t, c, 0, "initial_contact", "initial_contact", "major")
	return c
}
func checkNative(t *testing.T, c *NativeClock, minute int, kind, action, severity string) {
	t.Helper()
	at := epoch.Add(time.Duration(minute) * time.Minute)
	e := NativeEvent{Kind: kind, Source: c.Source, At: at, Token: "original", Target: c.Target}
	out, err := c.Apply(e)
	if err != nil {
		t.Fatal(err)
	}
	if action == "" {
		if out != nil {
			t.Fatalf("unexpected action %+v", out)
		}
		return
	}
	if out == nil || out.Kind != action || out.Severity != severity || out.Target != c.Target || !out.Due.Equal(at) {
		t.Fatalf("at minute %d expected %s/%s, got %+v", minute, action, severity, out)
	}
	e.Kind = "provider_outcome"
	e.Outcome = "confirmed"
	if _, err = c.Apply(e); err != nil {
		t.Fatal(err)
	}
}
func TestEscalationDoesNotNeedIncomingCallback(t *testing.T) {
	c := oracle(t, 30)
	checkNative(t, c, 10, "timer_due", "repeat", "major")
	checkNative(t, c, 20, "timer_due", "repeat", "major")
	checkNative(t, c, 30, "timer_due", "escalation", "critical")
	if c.Count != 1 {
		t.Fatal("timer manufactured a source occurrence")
	}
}
func TestCoincidentDeadlineSendsOnce(t *testing.T) {
	c := oracle(t, 30)
	for _, m := range []int{10, 20} {
		checkNative(t, c, m, "timer_due", "repeat", "major")
	}
	checkNative(t, c, 30, "timer_due", "escalation", "critical")
	checkNative(t, c, 30, "timer_due", "", "")
	checkNative(t, c, 40, "timer_due", "repeat", "critical")
}
func TestEscalationResetsRepeatClock(t *testing.T) {
	c := oracle(t, 35)
	for _, m := range []int{10, 20, 30} {
		checkNative(t, c, m, "timer_due", "repeat", "major")
	}
	checkNative(t, c, 35, "timer_due", "escalation", "critical")
	checkNative(t, c, 40, "timer_due", "", "")
	checkNative(t, c, 45, "timer_due", "repeat", "critical")
}
func TestSourceSeverityVariantKeepsEpisode(t *testing.T) {
	c := oracle(t, 30)
	e := NativeEvent{Kind: "source_observed", Source: c.Source, Severity: "minor", Fresh: true}
	if _, err := c.Apply(e); err != nil {
		t.Fatal(err)
	}
	e.Fresh = false
	if _, err := c.Apply(e); err != nil || c.Count != 2 {
		t.Fatal(err, c.Count)
	}
	e.Source.Generation++
	if _, err := c.Apply(e); err == nil {
		t.Fatal("lookalike attached")
	}
}
func TestSyntheticAutoTicketStillWorks(t *testing.T) {
	c := NativeClock{Target: "history"}
	e := NativeEvent{Kind: "new_episode", Source: SourceRef{"authority", "key", "synthetic", epoch.Format(time.RFC3339), 1}, At: epoch, Severity: "major", Synthetic: true, AutoTicket: true, Token: "original"}
	out, err := c.Apply(e)
	if err != nil || out == nil || out.Kind != "automatic_ticket" || out.TicketOutcome != "handler_mapped" {
		t.Fatal(out, err)
	}
	e.Kind = "provider_outcome"
	e.Outcome = "confirmed"
	e.Target = "history"
	if _, err = c.Apply(e); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"manual_ticket", "manual_resend", "timer_due"} {
		checkNative(t, &c, 30, k, "", "")
	}
}
func TestResolvedEpisodeCannotSend(t *testing.T) {
	c := oracle(t, 30)
	e := NativeEvent{Kind: "resolved", Source: c.Source}
	if _, err := c.Apply(e); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"manual_ticket", "manual_resend", "timer_due"} {
		checkNative(t, c, 30, k, "", "")
	}
}
func TestUnknownTicketDoesNotRecreate(t *testing.T) {
	c := oracle(t, 30)
	e := NativeEvent{Kind: "manual_ticket", Source: c.Source, At: epoch, Token: "original", Target: c.Target}
	if out, err := c.Apply(e); err != nil || out == nil {
		t.Fatal(out, err)
	}
	e.Kind = "provider_outcome"
	e.Outcome = "unknown"
	if _, err := c.Apply(e); err != nil {
		t.Fatal(err)
	}
	checkNative(t, c, 10, "manual_ticket", "", "")
	e.Token = "other"
	e.Outcome = "confirmed"
	if _, err := c.Apply(e); err == nil {
		t.Fatal("foreign settlement accepted")
	}
}
func TestEntryEnrollmentAndCrashOwnership(t *testing.T) {
	for _, order := range []bool{true, false} {
		for _, tracked := range []bool{true, false} {
			for _, failure := range []string{"crash", "unknown"} {
				e := EntryFact{"source", "entry", "one", tracked}
				enroll := EntryFact{"source", "enroll", "catalog", true}
				f := []EntryFact{e, enroll}
				if order {
					f = []EntryFact{enroll, e}
				}
				f = append(f, EntryFact{"source", failure, "one", tracked}, EntryFact{"sibling", "entry", "independent", true})
				replacement := append(append([]EntryFact{}, f...), EntryFact{"source", "entry", "two", true})
				held := tracked || failure == "unknown"
				if err := CheckEntryHistory(replacement); (err != nil) != held {
					t.Fatalf("%+v: %v", replacement, err)
				}
				if held {
					if err := CheckEntryHistory(append(f, EntryFact{"source", "confirmed", "two", true})); err == nil {
						t.Fatal("foreign owner settled original")
					}
					if err := CheckEntryHistory(append(f, EntryFact{"source", "confirmed", "one", true})); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}
func TestSuppressionKeepsActiveAgeAndDispatchBound(t *testing.T) {
	c := oracle(t, 30)
	e := NativeEvent{Kind: "suppression_changed", Source: c.Source, Suppressed: true}
	if _, err := c.Apply(e); err != nil {
		t.Fatal(err)
	}
	checkNative(t, c, 30, "timer_due", "", "")
	e.Suppressed = false
	if _, err := c.Apply(e); err != nil {
		t.Fatal(err)
	}
	e.Kind = "timer_due"
	e.At = epoch.Add(37 * time.Minute)
	e.Token = "original"
	out, err := c.Apply(e)
	if err != nil || out == nil || out.Due != epoch.Add(30*time.Minute) {
		t.Fatal(out, err)
	}
	for _, v := range []struct {
		offset time.Duration
		ok     bool
	}{{-251 * time.Millisecond, false}, {-249 * time.Millisecond, true}, {30 * time.Second, true}, {30*time.Second + 1, false}} {
		err := CheckDispatch(out.Due, e.At, e.At.Add(v.offset), 250*time.Millisecond)
		if (err == nil) != v.ok {
			t.Fatal(v, err)
		}
	}
}
