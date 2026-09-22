package sparse

import (
	"errors"
	"math"
	"testing"
)

func TestOrderErrorDecreasing(t *testing.T) {
	a := Vector{{1, 1}, {5, 2}, {3, 3}} // decreases at position 2
	b := Vector{{1, 1}}

	_, _, err := Dot(a, b)
	var oe *OrderError
	if !errors.As(err, &oe) {
		t.Fatalf("err = %v, want *OrderError", err)
	}
	if oe.Vector != 0 || oe.Position != 2 || oe.Prev != 5 || oe.Got != 3 {
		t.Fatalf("OrderError = %+v, want vector 0 position 2 prev 5 got 3", oe)
	}
}

func TestOrderErrorEqual(t *testing.T) {
	a := Vector{{1, 1}}
	b := Vector{{2, 1}, {2, 2}} // duplicate index at position 1

	_, _, err := Cosine(a, b)
	var oe *OrderError
	if !errors.As(err, &oe) {
		t.Fatalf("err = %v, want *OrderError", err)
	}
	if oe.Vector != 1 || oe.Position != 1 || oe.Prev != 2 || oe.Got != 2 {
		t.Fatalf("OrderError = %+v, want vector 1 position 1 prev 2 got 2", oe)
	}
}

func TestNaNError(t *testing.T) {
	a := Vector{{0, 1}, {4, math.NaN()}}
	b := Vector{{0, 1}}

	_, _, err := Dot(a, b)
	var ne *NaNError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want *NaNError", err)
	}
	if ne.Vector != 0 || ne.Position != 1 {
		t.Fatalf("NaNError = %+v, want vector 0 position 1", ne)
	}
}

func TestNaNErrorSecondVector(t *testing.T) {
	a := Vector{{0, 1}}
	b := Vector{{0, math.NaN()}}

	_, _, err := Cosine(a, b)
	var ne *NaNError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want *NaNError", err)
	}
	if ne.Vector != 1 || ne.Position != 0 {
		t.Fatalf("NaNError = %+v, want vector 1 position 0", ne)
	}
}
