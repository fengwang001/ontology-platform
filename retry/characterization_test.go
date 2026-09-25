package retry

import (
	"errors"
	"math"
	"math/big"
	"testing"
	"time"
)

// Characterization tests pin the CURRENT (possibly buggy) behavior of the
// retry scheduler around overflow saturation, float64 jitter conversion,
// the unreachable negative-factor clamp, and silent normalization clamps.
// Every assertion records what the implementation does today, not what the
// comments promise.

var (
	probeBase   = time.Duration(1) << 62
	probeMaxD   = time.Duration(math.MaxInt64)
	probeBelow1 = math.Nextafter(1, 0)
)

// TestBaseDelayOverflowWraparound pins the saturate-on-wrap rule
// (`next < d`). Multiplication is int64 two's complement wrap, so the
// guard only fires when the wrapped product lands below d. With Base=2^62
// the products wrap to -2^63 / -2^62 / 0 / +2^62 for Factor=2..5: the
// last one wraps BACK TO d and escapes saturation.
func TestBaseDelayOverflowWraparound(t *testing.T) {
	cases := []struct {
		factor int
		want   time.Duration
		note   string
	}{
		{2, probeMaxD, "2*2^62 wraps to -2^63 < d: saturates"},
		{3, probeMaxD, "3*2^62 wraps to -2^62 < d: saturates"},
		{4, probeMaxD, "4*2^62 wraps to 0 < d: saturates"},
		{5, probeBase, "5*2^62 wraps to 2^62 == d: guard misses, stays at Base"},
	}
	for _, tc := range cases {
		p := Policy{Base: probeBase, Factor: tc.factor}
		got := p.baseDelay(2)
		if got != tc.want {
			t.Errorf("factor=%d baseDelay(2)=%d, want %d (%s)",
				tc.factor, int64(got), int64(tc.want), tc.note)
		}
	}

	// Once factor=5 fixes d at 2^62, every later round re-wraps to the
	// same value: the delay freezes at Base instead of saturating.
	frozen := Policy{Base: probeBase, Factor: 5}.baseDelay(4)
	if frozen != probeBase {
		t.Errorf("baseDelay(4) with Factor=5 = %d, want frozen %d",
			int64(frozen), int64(probeBase))
	}
}

// exactJitter computes the mathematical Base*factor in arbitrary precision
// and returns it as an int64 Duration when it fits, reporting overflow
// otherwise. It mirrors the documented intent of delay() without float64.
func exactJitter(base time.Duration, factor float64) (time.Duration, bool) {
	bf := new(big.Float).SetPrec(256).SetInt64(int64(base))
	bf.Mul(bf, new(big.Float).SetPrec(256).SetFloat64(factor))
	bi, _ := bf.Int(nil)
	if bi.IsInt64() {
		return time.Duration(bi.Int64()), true
	}
	return 0, false
}

// TestDelayLargeBaseFloat64 pins the float64 round-trip used by delay().
// Above 2^53 the conversion loses integer precision, so the result does
// not equal the exact Base*factor; when the product exceeds MaxInt64 the
// float-to-Duration conversion saturates on this toolchain.
func TestDelayLargeBaseFloat64(t *testing.T) {
	const jitter = 100
	cases := []struct {
		name     string
		base     time.Duration
		x        float64
		saturate bool
	}{
		{"precision loss at 2^62+1, factor=1", 1<<62 + 1, 0.5, false},
		{"precision loss at 2^62+1, factor<2", 1<<62 + 1, probeBelow1, false},
		{"MaxInt64 base, factor=1 rounds float to 2^63", probeMaxD, 0.5, true},
		{"MaxInt64 base, factor<2 overflows", probeMaxD, probeBelow1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Policy{Base: tc.base, Factor: 1, JitterPct: jitter}
			j := float64(jitter) / 100
			factor := 1 + (2*tc.x-1)*j
			got := p.delay(1, func() float64 { return tc.x })

			// What the implementation actually computes: the float64
			// product converted back to Duration.
			wantFloat := time.Duration(float64(tc.base) * factor)
			if got != wantFloat {
				t.Fatalf("delay = %d, want float64 round-trip %d",
					int64(got), int64(wantFloat))
			}

			// The exact mathematical result; mismatch proves precision
			// loss. When the float64 product cannot fit int64, the
			// conversion saturates to MaxInt64 on this toolchain.
			exact, fits := exactJitter(tc.base, factor)
			if fits && !tc.saturate && got == exact {
				t.Errorf("delay %d unexpectedly equals exact value %d; "+
					"expected float64 precision loss", int64(got), int64(exact))
			}
			if tc.saturate || !fits {
				if got != probeMaxD {
					t.Errorf("overflowed delay = %d, want MaxInt64 saturation",
						int64(got))
				}
				if factor < 1 {
					t.Errorf("factor %v must exceed 1 in overflow case", factor)
				}
			}
		})
	}
}

// TestFactorClampUnreachableWithLegalInputs sweeps every legal
// JitterPct (0..100, clamped policy) against rnd values covering the
// documented [0,1) contract including its endpoints. factor =
// 1 + (2*rnd-1)*j is always >= 0, so the `factor < 0` guard is dead code
// for any conforming rnd.
func TestFactorClampUnreachableWithLegalInputs(t *testing.T) {
	samples := []float64{0, 0.25, 0.5, 0.75, probeBelow1}
	for pct := 0; pct <= 100; pct++ {
		p := Policy{Base: 1 << 62, Factor: 1, JitterPct: pct}
		for _, x := range samples {
			j := float64(pct) / 100
			factor := 1 + (2*x-1)*j
			if factor < 0 {
				t.Fatalf("factor %v reachable with legal rnd=%v pct=%d",
					factor, x, pct)
			}
			got := p.delay(1, func() float64 { return x })
			if got != time.Duration(float64(p.baseDelay(1))*factor) {
				t.Fatalf("pct=%d rnd=%v: delay %d != unclamped product",
					pct, x, int64(got))
			}
		}
	}

	// The clamp CAN only fire for an out-of-contract rnd (< 0 or >= 1),
	// which is not allowed by the documented [0,1) source. With such a
	// source the negative factor is flattened to 0.
	p := Policy{Base: time.Second, Factor: 1, JitterPct: 100}
	if got := p.delay(1, func() float64 { return -1 }); got != 0 {
		t.Fatalf("illegal rnd=-1: delay = %d, want 0 via dead-code clamp",
			int64(got))
	}
}

// TestNormalizedSilentClamps pins that invalid Policy fields are silently
// rewritten instead of rejected.
func TestNormalizedSilentClamps(t *testing.T) {
	cases := []struct {
		name    string
		in      Policy
		wantMax int
		wantFac int
		wantJit int
	}{
		{"zero attempts", Policy{MaxAttempts: 0, Factor: 3, JitterPct: 10}, 1, 3, 10},
		{"negative attempts", Policy{MaxAttempts: -7, Factor: 2}, 1, 2, 0},
		{"factor zero", Policy{MaxAttempts: 2, Factor: 0}, 2, 1, 0},
		{"factor negative", Policy{MaxAttempts: 2, Factor: -9}, 2, 1, 0},
		{"jitter negative", Policy{MaxAttempts: 2, Factor: 2, JitterPct: -50}, 2, 2, 0},
		{"jitter over 100", Policy{MaxAttempts: 2, Factor: 2, JitterPct: 150}, 2, 2, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.in.normalized()
			if n.MaxAttempts != tc.wantMax || n.Factor != tc.wantFac ||
				n.JitterPct != tc.wantJit {
				t.Fatalf("normalized() = MaxAttempts:%d Factor:%d JitterPct:%d, "+
					"want %d/%d/%d", n.MaxAttempts, n.Factor, n.JitterPct,
					tc.wantMax, tc.wantFac, tc.wantJit)
			}
		})
	}
}

// TestNormalizedClampsObservedViaDo pins the runtime effect of the silent
// clamps on attempts, growth, and jitter band.
func TestNormalizedClampsObservedViaDo(t *testing.T) {
	// MaxAttempts <= 0 runs exactly once: no waits, ErrExhausted.
	for _, max := range []int{0, -3} {
		r := New(Policy{MaxAttempts: max, Base: time.Second},
			func(time.Duration) {}, nil)
		runs := 0
		n, err := r.Do(func(int) error { runs++; return errBoom })
		if runs != 1 || n != 1 || !errors.Is(err, ErrExhausted) {
			t.Fatalf("MaxAttempts=%d: runs=%d n=%d err=%v", max, runs, n, err)
		}
		if len(r.Delays()) != 0 {
			t.Fatalf("MaxAttempts=%d: unexpected delays %v", max, r.Delays())
		}
	}

	// Factor <= 1 clamps to 1: delays stay flat at Base.
	for _, factor := range []int{0, -5} {
		r := New(Policy{MaxAttempts: 4, Base: 100, Factor: factor},
			func(time.Duration) {}, nil)
		r.Do(failAlways(nil))
		for i, d := range r.Delays() {
			if d != 100 {
				t.Fatalf("Factor=%d delay[%d]=%d, want flat 100", factor, i, d)
			}
		}
	}

	// JitterPct > 100 clamps to 100: rnd=0 yields factor 0 (zero wait);
	// JitterPct < 0 clamps to 0: rnd is never consulted and Base passes
	// through untouched.
	high := New(Policy{MaxAttempts: 2, Base: 1000, Factor: 1, JitterPct: 150},
		func(time.Duration) {}, func() float64 { return 0 })
	high.Do(failAlways(nil))
	if d := high.Delays(); len(d) != 1 || d[0] != 0 {
		t.Fatalf("JitterPct=150 rnd=0: delays=%v, want [0]", d)
	}

	drawn := false
	low := New(Policy{MaxAttempts: 2, Base: 1000, Factor: 1, JitterPct: -40},
		func(time.Duration) {}, func() float64 { drawn = true; return 0 })
	low.Do(failAlways(nil))
	if drawn {
		t.Fatal("JitterPct=-40: rnd consulted despite clamp to 0")
	}
	if d := low.Delays(); len(d) != 1 || d[0] != 1000 {
		t.Fatalf("JitterPct=-40: delays=%v, want [1000]", d)
	}
}
