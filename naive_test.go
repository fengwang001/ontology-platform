package parking

import (
	"errors"
	"math/rand"
	"testing"
)

func sameNaiveError(a, b error) bool {
	return errors.Is(a, b) || (a == nil && b == nil)
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	cfg := testConfig()
	lots := NewManager(6, cfg)
	naive := newNaive(6, cfg)
	rng := rand.New(rand.NewSource(20261006))
	plates := []string{"a", "b", "c", "d", "e", "f", "v1", "v2"}
	lastNow := 0
	for step := 0; step < 400; step++ {
		now := lastNow + rng.Intn(90)
		lastNow = now
		plate := plates[rng.Intn(len(plates))]
		var actual, expected naiveResult
		reason := ""

		switch rng.Intn(7) {
		case 0:
			start := now - rng.Intn(2)*Day
			end := now + (1+rng.Intn(4))*Day
			spot, err := lots.RegisterMonthly(now, plate, start, end)
			actual, expected = naiveResult{spot: spot, err: err}, naive.register(now, plate, start, end)
			reason = "register requires free public spot; scan in naive model"
			t.Logf("step=%d input=register now=%d plate=%s start=%d end=%d actual={spot:%d err:%v} naive={spot:%d err:%v} why=%s", step, now, plate, start, end, actual.spot, actual.err, expected.spot, expected.err, reason)
		case 1:
			end := now + (1+rng.Intn(5))*Day
			err := lots.RenewMonthly(now, plate, end)
			actual.err, expected.err = err, naive.renew(now, plate, end).err
			reason = "renew allowed before expiry or within grace days"
			t.Logf("step=%d input=renew now=%d plate=%s end=%d actual=%v naive=%v why=%s", step, now, plate, end, actual.err, expected.err, reason)
		case 2:
			spot := rng.Intn(6)
			interval := Interval{Start: rng.Intn(Day), End: rng.Intn(Day)}
			err := lots.SetShare(now, plate, spot, interval)
			actual.err, expected.err = err, naive.setShare(now, plate, spot, interval).err
			reason = "only spot owner can set a half-open daily share interval"
			t.Logf("step=%d input=setShare now=%d plate=%s spot=%d interval=%d-%d actual=%v naive=%v why=%s", step, now, plate, spot, interval.Start, interval.End, actual.err, expected.err, reason)
		case 3:
			spot, err := lots.MonthlyEnter(now, plate)
			actual, expected = naiveResult{spot: spot, err: err}, naive.monthlyEnter(now, plate)
			reason = "home first; otherwise public temporary spot; otherwise waiting and vacate demand"
			t.Logf("step=%d input=monthlyEnter now=%d plate=%s actual={spot:%d err:%v} naive={spot:%d err:%v} why=%s", step, now, plate, actual.spot, actual.err, expected.spot, expected.err, reason)
		case 4:
			spot, err := lots.VisitorEnter(now, plate)
			actual, expected = naiveResult{spot: spot, err: err}, naive.visitorEnter(now, plate)
			reason = "public spots have priority, then smallest free active shared monthly spot"
			t.Logf("step=%d input=visitorEnter now=%d plate=%s actual={spot:%d err:%v} naive={spot:%d err:%v} why=%s", step, now, plate, actual.spot, actual.err, expected.spot, expected.err, reason)
		case 5:
			result, err := lots.Exit(now, plate)
			actual = naiveResult{spot: result.Spot, fee: result.Fee, err: err}
			expected = naive.exit(now, plate)
			reason = "naive minute model computes free, capped base, vacate and share overtime"
			t.Logf("step=%d input=exit now=%d plate=%s actual={spot:%d fee:%d err:%v} naive={spot:%d fee:%d err:%v} why=%s", step, now, plate, actual.spot, actual.fee, actual.err, expected.spot, expected.fee, expected.err, reason)
		default:
			t.Logf("step=%d input=tick now=%d why=nondecreasing clock", step, now)
			continue
		}

		if actual.spot != expected.spot || actual.fee != expected.fee || !sameNaiveError(actual.err, expected.err) {
			t.Fatalf("mismatch at step %d: actual=%+v expected=%+v", step, actual, expected)
		}
	}
}
