package alloc

import (
	"reflect"
	"testing"
)

func sum(vs []int64) int64 {
	var s int64
	for _, v := range vs {
		s += v
	}
	return s
}

func TestAllocateCases(t *testing.T) {
	cases := []struct {
		name    string
		amount  int64
		weights []int64
		want    []int64
	}{
		{"exact", 100, []int64{1, 1}, []int64{50, 50}},
		{"proportional", 100, []int64{1, 3}, []int64{25, 75}},
		{"floor", 7, []int64{1, 1}, []int64{4, 3}},
		{"largestRemainder", 10, []int64{1, 1, 1}, []int64{4, 3, 3}},
		{"tieBreakByIndex", 5, []int64{1, 1, 1}, []int64{2, 2, 1}},
		{"zeroWeight", 10, []int64{0, 1, 1}, []int64{0, 5, 5}},
		{"zeroWeightRemainder", 7, []int64{0, 1, 1}, []int64{0, 4, 3}},
		{"negativeFloor", -7, []int64{1, 1}, []int64{-4, -3}},
		{"negativeTie", -5, []int64{1, 1, 1}, []int64{-2, -2, -1}},
		{"negativeZeroWeight", -7, []int64{0, 1, 1}, []int64{0, -4, -3}},
		{"singleWeight", -100, []int64{5}, []int64{-100}},
		{"zeroAmount", 0, []int64{3, 1}, []int64{0, 0}},
		{"bigRemainderOrder", 11, []int64{1, 2, 3}, []int64{2, 4, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Allocate(tc.amount, tc.weights)
			if err != nil {
				t.Fatalf("Allocate(%d, %v) error: %v", tc.amount, tc.weights, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Allocate(%d, %v) = %v, want %v", tc.amount, tc.weights, got, tc.want)
			}
			if sum(got) != tc.amount {
				t.Fatalf("conservation violated: sum=%d, amount=%d", sum(got), tc.amount)
			}
		})
	}
}

func TestAllocateMonotonicity(t *testing.T) {
	cases := []struct {
		amount  int64
		weights []int64
	}{
		{100, []int64{5, 3, 8, 1, 8}},
		{-100, []int64{5, 3, 8, 1, 8}},
		{1, []int64{7, 7, 2, 9}},
		{-1, []int64{7, 7, 2, 9}},
	}
	for _, tc := range cases {
		got, err := Allocate(tc.amount, tc.weights)
		if err != nil {
			t.Fatalf("Allocate(%d, %v) error: %v", tc.amount, tc.weights, err)
		}
		for i := 0; i < len(got); i++ {
			for j := 0; j < len(got); j++ {
				if tc.weights[i] <= tc.weights[j] {
					continue
				}
				if abs(got[i]) < abs(got[j]) {
					t.Fatalf("monotonicity violated: Allocate(%d, %v)=%v, i=%d j=%d",
						tc.amount, tc.weights, got, i, j)
				}
				if tc.amount >= 0 && got[i] < got[j] {
					t.Fatalf("literal monotonicity violated: Allocate(%d, %v)=%v, i=%d j=%d",
						tc.amount, tc.weights, got, i, j)
				}
			}
		}
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestAllocateDeterministic(t *testing.T) {
	weights := []int64{3, 3, 3, 1, 2}
	first, err := Allocate(-101, weights)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := Allocate(-101, weights)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("non-deterministic result: %v vs %v", got, first)
		}
	}
}

func TestAllocateDoesNotMutateWeights(t *testing.T) {
	weights := []int64{1, 2, 3}
	orig := append([]int64(nil), weights...)
	if _, err := Allocate(100, weights); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(weights, orig) {
		t.Fatalf("weights mutated: %v", weights)
	}
}
