package ontology

import (
	"math"
	"math/rand"
	"testing"
)

// sumFloats feeds values into a fresh groupSum in the given order and
// returns the float64 result.
func sumFloats(vals []float64) float64 {
	s := newGroupSum()
	for _, v := range vals {
		s.addValue(v, true)
	}
	return s.result().floatSum
}

// Order independence: shuffling the same multiset of values must yield
// bit-identical float64 sums, and the sum must sit near the true value.
func TestSumOrderIndependentBitExact(t *testing.T) {
	// Magnitudes spanning many exponents make naive left-to-right
	// accumulation order-dependent.
	vals := []float64{1e16, 1.0, -1e16, 0.1, 0.2, 0.3, 1e-9, -0.1, 42.5, 3.14159}
	const want = 1.0 + 0.1 + 0.2 + 0.3 + 1e-9 - 0.1 + 42.5 + 3.14159

	base := sumFloats(vals)
	baseBits := math.Float64bits(base)

	rng := rand.New(rand.NewSource(1))
	for round := 0; round < 200; round++ {
		shuffled := make([]float64, len(vals))
		copy(shuffled, vals)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := sumFloats(shuffled)
		if math.Float64bits(got) != baseBits {
			t.Fatalf("round %d: bits differ: got %x want %x",
				round, math.Float64bits(got), baseBits)
		}
	}
	// Absolute error bound: exact rational sum rounded once to float64
	// is within a few ulps of the true value; 1e-6 is generous here.
	if math.Abs(base-want) > 1e-6 {
		t.Fatalf("sum %v too far from true value %v", base, want)
	}
}

// NaN inputs are skipped and counted; they must not poison the group.
func TestSumNaNSkipped(t *testing.T) {
	s := newGroupSum()
	s.addValue(1.5, true)
	s.addValue(math.NaN(), true)
	s.addValue(2.5, true)
	res := s.result()
	if res.floatSum != 4.0 {
		t.Fatalf("got %v, want 4.0", res.floatSum)
	}
	if s.skipped != 1 {
		t.Fatalf("skipped = %d, want 1", s.skipped)
	}
}

// Infinities participate in the sum with IEEE 754 semantics.
func TestSumInfinities(t *testing.T) {
	pos := newGroupSum()
	pos.addValue(math.Inf(1), true)
	pos.addValue(1.0, true)
	if got := pos.result().floatSum; !math.IsInf(got, 1) {
		t.Fatalf("got %v, want +Inf", got)
	}

	neg := newGroupSum()
	neg.addValue(math.Inf(-1), true)
	neg.addValue(1.0, true)
	if got := neg.result().floatSum; !math.IsInf(got, -1) {
		t.Fatalf("got %v, want -Inf", got)
	}

	both := newGroupSum()
	both.addValue(math.Inf(1), true)
	both.addValue(math.Inf(-1), true)
	if got := both.result().floatSum; !math.IsNaN(got) {
		t.Fatalf("got %v, want NaN for +Inf + -Inf", got)
	}
	if both.skipped != 0 {
		t.Fatalf("infinities must not be skipped, skipped = %d", both.skipped)
	}
}

// Mixed int64/float64 in one group yields a float64 result.
func TestSumMixedIntFloat(t *testing.T) {
	s := newGroupSum()
	s.addValue(int64(2), true)
	s.addValue(0.5, true)
	res := s.result()
	if res.isInt {
		t.Fatal("mixed group must produce a float64 result")
	}
	if res.floatSum != 2.5 {
		t.Fatalf("got %v, want 2.5", res.floatSum)
	}
}

// An all-int64 group whose sum exceeds int64 reports overflow.
func TestSumInt64Overflow(t *testing.T) {
	s := newGroupSum()
	s.addValue(int64(math.MaxInt64), true)
	s.addValue(int64(1), true)
	if !s.result().overflow {
		t.Fatal("expected overflow to be reported")
	}

	ok := newGroupSum()
	ok.addValue(int64(math.MaxInt64), true)
	ok.addValue(int64(-1), true)
	res := ok.result()
	if res.overflow || !res.isInt || res.intSum != math.MaxInt64-1 {
		t.Fatalf("unexpected result %+v", res)
	}

	// A float64 arriving later makes the group mixed: no overflow.
	mixed := newGroupSum()
	mixed.addValue(int64(math.MaxInt64), true)
	mixed.addValue(int64(1), true)
	mixed.addValue(0.0, true)
	res = mixed.result()
	if res.overflow || res.isInt {
		t.Fatalf("mixed group must not overflow: %+v", res)
	}
	if res.floatSum != float64(math.MaxInt64) {
		t.Fatalf("got %v, want %v", res.floatSum, float64(math.MaxInt64))
	}
}

// Non-summable and missing values are skipped, not summed.
func TestSumSkipsNonSummable(t *testing.T) {
	s := newGroupSum()
	s.addValue(int64(5), true)
	s.addValue("nope", true)
	s.addValue(true, true)
	s.addValue(nil, false) // attribute absent
	res := s.result()
	if !res.isInt || res.intSum != 5 {
		t.Fatalf("unexpected result %+v", res)
	}
	if s.skipped != 3 {
		t.Fatalf("skipped = %d, want 3", s.skipped)
	}
}
