package alloc

import (
	"errors"
	"math"
	"testing"
)

func TestAllocateErrors(t *testing.T) {
	cases := []struct {
		name    string
		amount  int64
		weights []int64
		want    error
	}{
		{"empty", 100, nil, ErrEmptyWeights},
		{"negativeWeight", 100, []int64{1, -1}, ErrNegativeWeight},
		{"allZero", 100, []int64{0, 0, 0}, ErrZeroTotalWeight},
		{"productOverflow", math.MaxInt64, []int64{2, 1}, ErrOverflow},
		{"productOverflowNegative", math.MinInt64, []int64{2}, ErrOverflow},
		{"sumOverflow", 1, []int64{math.MaxInt64, 1}, ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Allocate(tc.amount, tc.weights)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Allocate(%d, %v) err=%v, want %v", tc.amount, tc.weights, err, tc.want)
			}
			if got != nil {
				t.Fatalf("expected nil result on error, got %v", got)
			}
		})
	}
}

func TestAllocateMinIntAmount(t *testing.T) {
	got, err := Allocate(math.MinInt64, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != math.MinInt64 {
		t.Fatalf("Allocate(MinInt64, [1]) = %v", got)
	}
}

func TestSplitInvalidParts(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		got, err := Split(100, n)
		if !errors.Is(err, ErrInvalidParts) {
			t.Fatalf("Split(100, %d) err=%v, want %v", n, err, ErrInvalidParts)
		}
		if got != nil {
			t.Fatalf("expected nil result on error, got %v", got)
		}
	}
}
