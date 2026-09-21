package sparsevec

import (
	"errors"
	"math"
	"testing"
)

func TestDotNotSortedEqualIndices(t *testing.T) {
	a := Vector{{5, 1}, {5, 2}}
	b := Vector{{5, 1}}
	_, _, err := Dot(a, b)
	if !errors.Is(err, ErrNotSorted) {
		t.Fatalf("err = %v, want ErrNotSorted", err)
	}
	ve, ok := err.(*Error)
	if !ok {
		t.Fatalf("err type = %T, want *Error", err)
	}
	if ve.Vector != 0 || ve.Position != 1 {
		t.Fatalf("location = vector %d pos %d, want vector 0 pos 1", ve.Vector, ve.Position)
	}
}

func TestDotNotSortedDecreasingInSecondVector(t *testing.T) {
	a := Vector{{1, 1}, {2, 2}}
	b := Vector{{7, 1}, {3, 2}}
	_, _, err := Dot(a, b)
	if !errors.Is(err, ErrNotSorted) {
		t.Fatalf("err = %v, want ErrNotSorted", err)
	}
	ve := err.(*Error)
	if ve.Vector != 1 || ve.Position != 1 {
		t.Fatalf("location = vector %d pos %d, want vector 1 pos 1", ve.Vector, ve.Position)
	}
}

func TestDotNaNValue(t *testing.T) {
	a := Vector{{0, 1}, {4, math.NaN()}}
	b := Vector{{4, 1}}
	_, _, err := Dot(a, b)
	if !errors.Is(err, ErrNaNValue) {
		t.Fatalf("err = %v, want ErrNaNValue", err)
	}
	ve := err.(*Error)
	if ve.Vector != 0 || ve.Position != 1 {
		t.Fatalf("location = vector %d pos %d, want vector 0 pos 1", ve.Vector, ve.Position)
	}
}

// Explicit zeros are legal, counted in Stats, and must not change the dot.
func TestDotExplicitZerosCounted(t *testing.T) {
	withZeros := Vector{{0, 0}, {1, 2}, {5, 0}}
	plain := Vector{{1, 2}}
	b := Vector{{1, 3}, {2, 0}}
	dotZ, st, err := Dot(withZeros, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if st.ZerosA != 2 || st.ZerosB != 1 || st.TotalZeros() != 3 {
		t.Fatalf("zeros = %+v, want ZerosA=2 ZerosB=1", st)
	}
	dotP, _, err := Dot(plain, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if dotZ != dotP || dotZ != 6 {
		t.Fatalf("dot with zeros = %v, plain = %v, want 6", dotZ, dotP)
	}
}

func TestDotEmptyVectors(t *testing.T) {
	dot, st, err := Dot(nil, Vector{{3, 1}})
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if dot != 0 || st.Steps != 0 {
		t.Fatalf("dot = %v steps = %d, want 0 and 0", dot, st.Steps)
	}
}
