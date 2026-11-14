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
	e, err := Expect(p, ds)
	if err != nil {
		panic(err)
	}
	return e
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
	p := scenario.PolicySpec{Mode: "time", Interval: 110 * time.Second, Fresh: false, MaxResends: ip(5)}
	// R1 is fed unchanged repeats, as in the real scenario
	check(t, run(p, func(i int) bool { return i == 0 }), mins(2, 4, 6, 8, 10), true)
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
	p := scenario.PolicySpec{Mode: "time", Interval: 590 * time.Second, MaxResends: ip(2)}
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
	got, _ := Expect(p, []Delivery{{0, true}, {60 * time.Second, true}, {120 * time.Second, true},
		{180 * time.Second, true}, {240 * time.Second, true}, {300 * time.Second, true}, {360 * time.Second, true}})
	want := []time.Duration{120 * time.Second, 240 * time.Second, 300 * time.Second}
	if !reflect.DeepEqual(got.Resends, want) {
		t.Fatalf("got %v want %v", got.Resends, want)
	}
}

func TestOccDueCrossedMultipleStillFires(t *testing.T) {
	if !occDue(7, 3, 1) || occDue(5, 3, 1) || occDue(6, 3, 2) {
		t.Fatal("occDue must fire once per crossed, unhandled multiple regardless of exact count")
	}
}

func TestUnknownModeErrors(t *testing.T) {
	if _, err := Expect(scenario.PolicySpec{}, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestEveryOneInitialHandled(t *testing.T) {
	p := scenario.PolicySpec{Mode: "occurrences", Every: 1}
	check(t, run(p, func(i int) bool { return i == 0 }), nil, false)
	all := make([]int, 28)
	for i := range all {
		all[i] = i + 1
	}
	check(t, run(p, allFresh), mins(all...), false)
}

// Time resend at count 4 (between multiples of 3) must not consume the
// multiple at count 6, which still fires on its own delivery.
// Note: with +1 counting, `>` and `==` are indistinguishable through Expect;
// occDue's own test pins `>`.
func TestEitherTimeResendThenCrossing(t *testing.T) {
	p := scenario.PolicySpec{Mode: "either", Interval: 90 * time.Second, Every: 3}
	var ds []Delivery
	for i := 0; i < 8; i++ {
		ds = append(ds, Delivery{At: time.Duration(i) * 60 * time.Second, Fresh: true})
	}
	got, _ := Expect(p, ds)
	// 2m: count3 (+time); 4m: time, count5; 5m: count6; 7m: time(120s), count8
	want := mins(2, 4, 5, 7)
	if !reflect.DeepEqual(got.Resends, want) {
		t.Fatalf("got %v want %v", got.Resends, want)
	}
}
