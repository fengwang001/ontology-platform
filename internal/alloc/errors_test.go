package alloc

import (
	"errors"
	"math"
	"testing"
)

func TestAllocateEmptyWeights(t *testing.T) {
	got, err := Allocate(100, nil)
	if !errors.Is(err, ErrNoWeights) {
		t.Fatalf("got %v, want ErrNoWeights", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil result", got)
	}

	got, err = Allocate(100, []int64{})
	if !errors.Is(err, ErrNoWeights) {
		t.Fatalf("got %v, want ErrNoWeights", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil result", got)
	}
}

func TestAllocateNegativeWeight(t *testing.T) {
	for _, weights := range [][]int64{
		{-1},
		{1, -2, 3},
		{0, -1, 0},
		{math.MinInt64, 1},
	} {
		got, err := Allocate(100, weights)
		if !errors.Is(err, ErrNegativeWeight) {
			t.Fatalf("weights=%v: got %v, want ErrNegativeWeight", weights, err)
		}
		if got != nil {
			t.Fatalf("weights=%v: got %v, want nil", weights, got)
		}
	}
}

func TestAllocateZeroTotalWeight(t *testing.T) {
	for _, weights := range [][]int64{
		{0},
		{0, 0, 0},
	} {
		got, err := Allocate(100, weights)
		if !errors.Is(err, ErrZeroTotalWeight) {
			t.Fatalf("weights=%v: got %v, want ErrZeroTotalWeight", weights, err)
		}
		if got != nil {
			t.Fatalf("weights=%v: got %v, want nil", weights, got)
		}
	}
	// 负金额同样要报错，而不是静默给出 0。
	got, err := Allocate(-100, []int64{0, 0})
	if !errors.Is(err, ErrZeroTotalWeight) {
		t.Fatalf("got %v, want ErrZeroTotalWeight", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestAllocateOverflow(t *testing.T) {
	cases := []struct {
		amount  int64
		weights []int64
	}{
		{math.MaxInt64, []int64{2}},
		{math.MinInt64, []int64{2}},
		{math.MaxInt64, []int64{1, 2}},
		{1 << 40, []int64{1 << 40}},
	}
	for _, tc := range cases {
		got, err := Allocate(tc.amount, tc.weights)
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("amount=%d weights=%v: got %v, want ErrOverflow",
				tc.amount, tc.weights, err)
		}
		if got != nil {
			t.Fatalf("amount=%d weights=%v: got %v, want nil",
				tc.amount, tc.weights, got)
		}
	}
}

func TestAllocateWeightSumOverflow(t *testing.T) {
	// 权重之和本身溢出 int64。
	got, err := Allocate(1, []int64{math.MaxInt64, math.MaxInt64, 1})
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
