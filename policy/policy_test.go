package policy

import (
	"errors"
	"math"
	"testing"
)

func TestValidateOK(t *testing.T) {
	q, err := Validate(10, 2.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Capacity != 10 || q.RatePerSec != 2.5 {
		t.Fatalf("quota = %+v, want {10 2.5}", q)
	}
	if err := q.Check(); err != nil {
		t.Fatalf("Check on valid quota: %v", err)
	}
}

func TestValidateCapacityBoundaries(t *testing.T) {
	bad := []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, c := range bad {
		if _, err := Validate(c, 1); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("capacity %v: err = %v, want ErrInvalidCapacity", c, err)
		}
	}
	if _, err := Validate(math.SmallestNonzeroFloat64, 1); err != nil {
		t.Fatalf("tiny positive capacity should be legal: %v", err)
	}
}

func TestValidateRateBoundaries(t *testing.T) {
	bad := []float64{0, -0.5, math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, r := range bad {
		if _, err := Validate(1, r); !errors.Is(err, ErrInvalidRate) {
			t.Fatalf("rate %v: err = %v, want ErrInvalidRate", r, err)
		}
	}
}

func TestCheckOnZeroValue(t *testing.T) {
	var q Quota
	if err := q.Check(); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("zero quota: err = %v, want ErrInvalidCapacity", err)
	}
}
