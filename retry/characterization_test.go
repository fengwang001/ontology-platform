package retry

import (
	"errors"
	"testing"
	"time"
)

const maxDuration = time.Duration(1<<63 - 1)

// Characterization: baseDelay's overflow saturation uses `next < d` to
// detect wraparound. These tests pin the CURRENT behavior of that check,
// including its blind spots; they are not statements of desired behavior.
func TestBaseDelayOverflowSaturationBlindSpot(t *testing.T) {
	cases := []struct {
		name   string
		base   time.Duration
		factor int
		k      int
		want   time.Duration
	}{
		// Base = 2^62: factors whose product wraps to a value < d (or
		// negative) are caught and saturate to MaxInt64.
		{"wrap-to-negative/factor2", 1 << 62, 2, 2, maxDuration},
		{"wrap-to-negative/factor3", 1 << 62, 3, 2, maxDuration},
		{"wrap-to-zero/factor4", 1 << 62, 4, 2, maxDuration},
		{"wrap-to-negative/factor6", 1 << 62, 6, 2, maxDuration},
		{"wrap-to-zero/factor8", 1 << 62, 8, 2, maxDuration},
		// Factors == 1 (mod 4): 5*2^62, 9*2^62, 13*2^62 all wrap back to
		// exactly d, so `next < d` is false and saturation does NOT fire,
		// even though the true product overflows int64.
		{"blind-spot-wrap-to-d/factor5", 1 << 62, 5, 2, 1 << 62},
		{"blind-spot-wrap-to-d/factor9", 1 << 62, 9, 2, 1 << 62},
		{"blind-spot-wrap-to-d/factor13", 1 << 62, 13, 2, 1 << 62},
		// Wrap can also land strictly above d: 5*(2^62+2^59) mod 2^64 =
		// 2^62+5*2^59, which is > d, so the check passes and baseDelay
		// returns a wrapped (wrong) "growth" value instead of MaxInt64.
		{"blind-spot-wrap-above-d/factor5", 1<<62 + 1<<59, 5, 2, 7493989779944505344},
		// The same `next < d` check misfires on negative Base: a negative
		// product is read as "overflow" and saturates UP to MaxInt64.
		{"negative-base-misread-as-overflow", -100, 2, 2, maxDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Policy{Base: tc.base, Factor: tc.factor}
			if got := p.baseDelay(tc.k); got != tc.want {
				t.Fatalf("baseDelay(%d) = %d, want %d", tc.k, got, tc.want)
			}
		})
	}

	// With Base = 2^62 and Factor = 5 the missed saturation also freezes
	// the schedule: every round wraps back to d, so there is no growth.
	p := Policy{Base: 1 << 62, Factor: 5}
	for k := 1; k <= 4; k++ {
		if got := p.baseDelay(k); got != 1<<62 {
			t.Fatalf("baseDelay(%d) = %d, want %d (frozen at Base)", k, got, int64(1<<62))
		}
	}
}

// Characterization: delay routes through float64, so durations above 2^53
// lose integer precision, and out-of-range float64->Duration conversion is
// platform-defined (saturating to MaxInt64 on this platform). Pins the
// CURRENT lossy results, not the mathematically exact ones.
func TestDelayFloat64PrecisionLoss(t *testing.T) {
	cases := []struct {
		name string
		base time.Duration
		rnd  float64
		want time.Duration
	}{
		// Below 2^53 the float64 round-trip is exact (control case).
		{"exact-below-2^53", 1000, 0.5, 1000},
		// factor == 1.0 exactly (rnd = 0.5), yet 2^53+1 is not
		// representable in float64 and comes back rounded down by 1.
		{"factor1-loses-lsb", 1<<53 + 1, 0.5, 1 << 53},
		// MaxInt64-100 rounds UP to 2^63 in float64, which converts back
		// to MaxInt64: the result is 100 ns larger than the input.
		{"factor1-rounds-up-to-max", maxDuration - 100, 0.5, maxDuration},
		// factor == 0.5: float64(MaxInt64) rounds to 2^63 first, so the
		// halved result is 2^62, one above floor(MaxInt64/2).
		{"factor-half-off-by-one", maxDuration, 0.25, 1 << 62},
		// factor > 1 with d near MaxInt64 overflows the float64->int64
		// conversion; on this platform it saturates to MaxInt64.
		{"factor1.5-saturates", maxDuration, 0.75, maxDuration},
		{"factor~2-saturates", maxDuration, 0.9999999999999999, maxDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Policy{Base: tc.base, JitterPct: 100}
			calls := 0
			rnd := func() float64 { calls++; return tc.rnd }
			if got := p.delay(1, rnd); got != tc.want {
				t.Fatalf("delay(1) = %d, want %d", got, tc.want)
			}
			if calls != 1 {
				t.Fatalf("rnd called %d times, want exactly 1", calls)
			}
		})
	}
}

// Characterization: the `if factor < 0 { factor = 0 }` guard in delay is
// dead code. With rnd in [0,1) and j = JitterPct/100 in [0,1],
// factor = 1 + (2*rnd-1)*j >= 1 - j >= 0 always. This grid pins that no
// legal (rnd, JitterPct) combination ever produces a negative delay, i.e.
// the guard can never trigger.
func TestDelayFactorNeverNegative(t *testing.T) {
	rnds := []float64{0, 1e-12, 0.25, 0.5, 0.75, 1 - 1e-12}
	jitterPcts := []int{0, 1, 50, 99, 100}
	for _, rnd := range rnds {
		for _, jp := range jitterPcts {
			p := Policy{Base: 1000, JitterPct: jp}
			got := p.delay(1, func() float64 { return rnd })
			if got < 0 {
				t.Fatalf("delay(1) = %d < 0 for rnd=%v JitterPct=%d", got, rnd, jp)
			}
			// At rnd == 0 and JitterPct == 100 the factor bottoms out at
			// exactly 0, the closest the guard ever gets to firing.
			if rnd == 0 && jp == 100 && got != 0 {
				t.Fatalf("delay(1) = %d, want 0 at factor minimum", got)
			}
		}
	}
}

// Characterization: normalized silently clamps invalid fields instead of
// reporting an error, so callers cannot distinguish "user supplied an
// illegal value" from "default". Pins both the clamped values and the
// resulting Do behavior.
func TestNormalizedSilentClamp(t *testing.T) {
	cases := []struct {
		name string
		in   Policy
		want Policy
	}{
		{"zero-values", Policy{}, Policy{MaxAttempts: 1, Factor: 1}},
		{"negative-attempts-and-factor", Policy{MaxAttempts: -3, Factor: -2, JitterPct: -5},
			Policy{MaxAttempts: 1, Factor: 1, JitterPct: 0}},
		{"jitter-over-100", Policy{MaxAttempts: 5, Factor: 3, JitterPct: 250},
			Policy{MaxAttempts: 5, Factor: 3, JitterPct: 100}},
		{"jitter-101", Policy{MaxAttempts: 2, Factor: 2, JitterPct: 101},
			Policy{MaxAttempts: 2, Factor: 2, JitterPct: 100}},
		{"valid-values-untouched", Policy{MaxAttempts: 4, Factor: 2, JitterPct: 99},
			Policy{MaxAttempts: 4, Factor: 2, JitterPct: 99}},
		// Base and Cap are NOT clamped: negative values pass through.
		{"negative-base-cap-pass-through", Policy{Base: -100, Cap: -5},
			Policy{MaxAttempts: 1, Factor: 1, Base: -100, Cap: -5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.normalized(); got != tc.want {
				t.Fatalf("normalized() = %+v, want %+v", got, tc.want)
			}
		})
	}

	// MaxAttempts <= 0: exactly one attempt, no waits, ErrExhausted.
	runs := 0
	r := New(Policy{MaxAttempts: -3, Base: time.Second}, func(time.Duration) {}, nil)
	n, err := r.Do(func(int) error { runs++; return errBoom })
	if runs != 1 || n != 1 || !errors.Is(err, ErrExhausted) || len(r.Delays()) != 0 {
		t.Fatalf("runs=%d n=%d err=%v delays=%v", runs, n, err, r.Delays())
	}

	// Factor <= 1: clamped to 1, so the schedule is flat.
	flat := New(Policy{MaxAttempts: 3, Base: 100, Factor: -2}, func(time.Duration) {}, nil)
	flat.Do(failAlways(nil))
	for i, d := range flat.Delays() {
		if d != 100 {
			t.Fatalf("delay[%d] = %v, want 100 (Factor clamped to 1)", i, d)
		}
	}

	// JitterPct < 0: clamped to 0, so rnd is never consulted and the raw
	// base delay is used.
	rndCalls := 0
	noJitter := New(Policy{MaxAttempts: 2, Base: 1000, JitterPct: -5},
		func(time.Duration) {}, func() float64 { rndCalls++; return 0.9 })
	noJitter.Do(failAlways(nil))
	if rndCalls != 0 {
		t.Fatalf("rnd called %d times, want 0 (JitterPct clamped to 0)", rndCalls)
	}
	if got := noJitter.Delays()[0]; got != 1000 {
		t.Fatalf("delay = %v, want 1000 (jitter disabled)", got)
	}

	// JitterPct > 100: clamped to 100. With rnd = 0.2 the clamped factor
	// is 1 + (0.4-1)*1 = 0.4 -> 400; unclamped j = 2.5 would give a
	// negative factor and (via the dead guard) 0.
	clamped := New(Policy{MaxAttempts: 2, Base: 1000, JitterPct: 250},
		func(time.Duration) {}, func() float64 { return 0.2 })
	clamped.Do(failAlways(nil))
	if got := clamped.Delays()[0]; got != 400 {
		t.Fatalf("delay = %v, want 400 (JitterPct clamped to 100)", got)
	}
}
