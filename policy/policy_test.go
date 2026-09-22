package policy

import (
	"errors"
	"math"
	"testing"
)

func TestNormalizeValid(t *testing.T) {
	q, err := Normalize(100, 5.5)
	if err != nil {
		t.Fatalf("valid quota rejected: %v", err)
	}
	if q.Capacity != 100 || q.RatePerSec != 5.5 {
		t.Fatalf("got %+v", q)
	}
	// Zero rate is legal: the bucket simply never refills.
	if _, err := Normalize(1, 0); err != nil {
		t.Fatalf("zero rate should be legal: %v", err)
	}
}

func TestNormalizeInvalidCapacity(t *testing.T) {
	for _, c := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Normalize(c, 1); !errors.Is(err, ErrCapacity) {
			t.Fatalf("capacity %v: want ErrCapacity, got %v", c, err)
		}
	}
}

func TestNormalizeInvalidRate(t *testing.T) {
	for _, r := range []float64{-0.1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Normalize(10, r); !errors.Is(err, ErrRate) {
			t.Fatalf("rate %v: want ErrRate, got %v", r, err)
		}
	}
}

func TestMustPanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Must should panic on invalid quota")
		}
	}()
	Must(-1, 1)
}
