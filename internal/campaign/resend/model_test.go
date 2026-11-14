package resend

import (
	"reflect"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

func run(p scenario.PolicySpec, fresh func(i int) bool) Expected {
	var ds []Delivery
	for i := 0; i <= 28; i++ {
		ds = append(ds, Delivery{At: time.Duration(i) * time.Minute, Fresh: fresh(i)})
	}
	return Expect(p, ds)
}

func allFresh(int) bool { return true }
func ip(i int) *int     { return &i }
func mins(m ...int) []time.Duration {
	var out []time.Duration
	for _, v := range m {
		out = append(out, time.Duration(v)*time.Minute)
	}
	return out
}

func check(t *testing.T, got Expected, want []time.Duration, exhausted bool) {
	t.Helper()
	if !reflect.DeepEqual(got.Resends, want) || got.Exhausted != exhausted || got.InitialAt != 0 {
		t.Fatalf("got %+v want resends %v exhausted %v", got, want, exhausted)
	}
}

func TestR1TimeEvery120Max5(t *testing.T) {
	p := scenario.PolicySpec{Mode: "time", Interval: 120 * time.Second, MaxResends: ip(5)}
	// deliveries are 60s apart, so 120s gaps land on even minutes
	check(t, run(p, allFresh), mins(2, 4, 6, 8, 10), true)
}

func TestR2OccurrencesEvery3Uncapped(t *testing.T) {
	p := scenario.PolicySpec{Mode: "occurrences", Every: 3}
	check(t, run(p, allFresh), mins(2, 5, 8, 11, 14, 17, 20, 23, 26), false)
}

func TestR3EitherCap2(t *testing.T) {
	p := scenario.PolicySpec{Mode: "either", Interval: 300 * time.Second, Every: 5, MaxResends: ip(2)}
	check(t, run(p, allFresh), mins(4, 9), true)
}

func TestR4Time600Max2(t *testing.T) {
	p := scenario.PolicySpec{Mode: "time", Interval: 600 * time.Second, MaxResends: ip(2)}
	check(t, run(p, allFresh), mins(10, 20), true)
}

func TestUnchangedRepeatsDoNotCountOccurrences(t *testing.T) {
	p := scenario.PolicySpec{Mode: "occurrences", Every: 3}
	check(t, run(p, func(i int) bool { return i == 0 }), nil, false)
}

func TestMaxResendsZeroInitialOnly(t *testing.T) {
	p := scenario.PolicySpec{Mode: "time", Interval: time.Minute, MaxResends: ip(0)}
	check(t, run(p, allFresh), nil, true)
}

// A time-triggered resend at a non-multiple count must not swallow or
// duplicate later multiples: every 3, 100s. Resends: 2 (both), 4 (time,
// count 5), 5 (occurrence, count 6).
func TestEitherTimeBetweenMultiples(t *testing.T) {
	p := scenario.PolicySpec{Mode: "either", Interval: 100 * time.Second, Every: 3}
	got := Expect(p, []Delivery{{0, true}, {60 * time.Second, true}, {120 * time.Second, true},
		{180 * time.Second, true}, {240 * time.Second, true}, {300 * time.Second, true}, {360 * time.Second, true}})
	want := []time.Duration{120 * time.Second, 240 * time.Second, 300 * time.Second}
	if !reflect.DeepEqual(got.Resends, want) {
		t.Fatalf("got %v want %v", got.Resends, want)
	}
}
