package policy

import (
	"errors"
	"math"
	"testing"
)

func TestNormalizeValid(t *testing.T) {
	q, err := Normalize(Quota{Capacity: 10, RatePerSec: 5})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if q.Capacity != 10 || q.RatePerSec != 5 {
		t.Fatalf("got %+v", q)
	}
}

func TestNormalizeRoundsCapacityUp(t *testing.T) {
	q, err := Normalize(Quota{Capacity: 3.2, RatePerSec: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if q.Capacity != 4 {
		t.Fatalf("capacity = %v, want 4 (ceil)", q.Capacity)
	}
}

func TestNormalizeZeroRateAllowed(t *testing.T) {
	q, err := Normalize(Quota{Capacity: 5, RatePerSec: 0})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if q.RatePerSec != 0 {
		t.Fatalf("rate = %v, want 0", q.RatePerSec)
	}
}

func TestNormalizeRejectsBadCapacity(t *testing.T) {
	for _, cap := range []float64{0, -1, 0.5, MaxCapacity + 1} {
		if _, err := Normalize(Quota{Capacity: cap, RatePerSec: 1}); !errors.Is(err, ErrCapacityRange) {
			t.Fatalf("capacity %v: got %v, want ErrCapacityRange", cap, err)
		}
	}
}

func TestNormalizeRejectsNonFinite(t *testing.T) {
	cases := []Quota{
		{Capacity: math.NaN(), RatePerSec: 1},
		{Capacity: math.Inf(1), RatePerSec: 1},
		{Capacity: 1, RatePerSec: math.NaN()},
		{Capacity: 1, RatePerSec: math.Inf(-1)},
	}
	for _, q := range cases {
		if _, err := Normalize(q); !errors.Is(err, ErrNonFinite) {
			t.Fatalf("%+v: got %v, want ErrNonFinite", q, err)
		}
	}
}

func TestNormalizeRejectsNegativeRate(t *testing.T) {
	if _, err := Normalize(Quota{Capacity: 5, RatePerSec: -0.1}); !errors.Is(err, ErrNegativeRate) {
		t.Fatalf("got %v, want ErrNegativeRate", err)
	}
}

func TestNormalizeClampsHugeRate(t *testing.T) {
	q, err := Normalize(Quota{Capacity: 5, RatePerSec: 1e15})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if q.RatePerSec != MaxRatePerSec {
		t.Fatalf("rate = %v, want clamped to %v", q.RatePerSec, MaxRatePerSec)
	}
}

func TestMustNormalizePanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MustNormalize(Quota{Capacity: -1, RatePerSec: 1})
}
