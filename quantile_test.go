package quantile

import (
	"errors"
	"math"
	"testing"
)

func bits(v float64) uint64 { return math.Float64bits(v) }

func TestDefinitionsDifferBetweenSamplesAgreeOnSample(t *testing.T) {
	s := NewSketch()
	for _, v := range []float64{10, 20, 30, 40} {
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}

	// p=0.5 falls strictly between samples: nearest rank is real data while
	// R-7 interpolates; the two must not be bit-identical.
	nr, err := s.QuantileNearestRank(0.5)
	if err != nil {
		t.Fatal(err)
	}
	lin, err := s.QuantileLinear(0.5)
	if err != nil {
		t.Fatal(err)
	}
	if nr != 20 {
		t.Fatalf("nearest rank = %v, want 20", nr)
	}
	if lin != 25 {
		t.Fatalf("linear = %v, want 25", lin)
	}
	if bits(nr) == bits(lin) {
		t.Fatalf("definitions must differ between samples, both %v", nr)
	}

	// p=1/3 lands exactly on an order statistic (h = 2): both definitions must
	// agree bit for bit and return a real sample.
	for _, p := range []float64{0, 1.0 / 3.0, 2.0 / 3.0, 1} {
		a, err := s.QuantileNearestRank(p)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.QuantileLinear(p)
		if err != nil {
			t.Fatal(err)
		}
		if bits(a) != bits(b) {
			t.Fatalf("p=%v: definitions differ on sample: %v vs %v", p, a, b)
		}
	}
}

func TestProbabilityBoundaries(t *testing.T) {
	s := NewSketch()
	for _, v := range []float64{5, 1, 9} {
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []func(*Sketch, float64) (float64, error){
		(*Sketch).QuantileNearestRank,
		(*Sketch).QuantileLinear,
	} {
		min, err := q(s, 0)
		if err != nil || min != 1 {
			t.Fatalf("p=0: v=%v err=%v, want 1", min, err)
		}
		max, err := q(s, 1)
		if err != nil || max != 9 {
			t.Fatalf("p=1: v=%v err=%v, want 9", max, err)
		}
	}
}

func TestInvalidProbability(t *testing.T) {
	s := NewSketch()
	if err := s.Add(1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := s.QuantileNearestRank(p); !errors.Is(err, ErrInvalidProbability) {
			t.Fatalf("p=%v nearest: got %v, want ErrInvalidProbability", p, err)
		}
		if _, err := s.QuantileLinear(p); !errors.Is(err, ErrInvalidProbability) {
			t.Fatalf("p=%v linear: got %v, want ErrInvalidProbability", p, err)
		}
	}
}

func TestEmptyDistinctFromInvalid(t *testing.T) {
	s := NewSketch()
	if _, err := s.QuantileNearestRank(0.5); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty nearest: got %v, want ErrEmpty", err)
	}
	if _, err := s.QuantileLinear(0.5); !errors.Is(err, ErrEmpty) {
		t.Fatalf(" empty linear: got %v, want ErrEmpty", err)
	}
	if errors.Is(ErrEmpty, ErrInvalidProbability) || errors.Is(ErrInvalidProbability, ErrEmpty) {
		t.Fatal("empty and invalid-probability errors must be distinguishable")
	}
	// Invalid p on an empty sketch is reported as a parameter error, never
	// silently clamped to a boundary.
	if _, err := s.QuantileLinear(2); !errors.Is(err, ErrInvalidProbability) {
		t.Fatalf("invalid p on empty: got %v", err)
	}
}

func TestSingleton(t *testing.T) {
	s := NewSketch()
	if err := s.Add(42); err != nil {
		t.Fatal(err)
	}
	for _, p := range []float64{0, 0.123, 0.5, 0.999, 1} {
		a, err := s.QuantileNearestRank(p)
		if err != nil || a != 42 {
			t.Fatalf("p=%v nearest %v %v", p, a, err)
		}
		b, err := s.QuantileLinear(p)
		if err != nil || bits(b) != bits(a) {
			t.Fatalf("p=%v linear %v %v", p, b, err)
		}
	}
}

func TestInfinities(t *testing.T) {
	s := NewSketch()
	for _, v := range []float64{math.Inf(-1), 1, math.Inf(1)} {
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	// A rank exactly on an infinity returns that infinity.
	v, err := s.QuantileLinear(1)
	if err != nil || !math.IsInf(v, 1) {
		t.Fatalf("p=1 = %v, %v", v, err)
	}
	v, err = s.QuantileLinear(0)
	if err != nil || !math.IsInf(v, -1) {
		t.Fatalf("p=0 = %v, %v", v, err)
	}
	// Between a finite value and +Inf: interpolation must be +Inf, not NaN.
	v, err = s.QuantileLinear(0.9)
	if err != nil || !math.IsInf(v, 1) {
		t.Fatalf("finite/+Inf interpolation = %v, %v want +Inf", v, err)
	}
	// Between -Inf and finite: interpolation must be -Inf.
	v, err = s.QuantileLinear(0.1)
	if err != nil || !math.IsInf(v, -1) {
		t.Fatalf("-Inf/finite interpolation = %v, %v want -Inf", v, err)
	}
}
