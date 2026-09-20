package alloc

import (
	"reflect"
	"testing"
)

func TestSplitCases(t *testing.T) {
	cases := []struct {
		name   string
		amount int64
		n      int
		want   []int64
	}{
		{"even", 10, 2, []int64{5, 5}},
		{"remainder", 10, 3, []int64{4, 3, 3}},
		{"negative", -10, 3, []int64{-4, -3, -3}},
		{"single", 42, 1, []int64{42}},
		{"zero", 0, 4, []int64{0, 0, 0, 0}},
		{"morePartsThanUnits", 2, 5, []int64{1, 1, 0, 0, 0}},
		{"negativeMoreParts", -2, 5, []int64{-1, -1, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(tc.amount, tc.n)
			if err != nil {
				t.Fatalf("Split(%d, %d) error: %v", tc.amount, tc.n, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Split(%d, %d) = %v, want %v", tc.amount, tc.n, got, tc.want)
			}
			if sum(got) != tc.amount {
				t.Fatalf("conservation violated: sum=%d, amount=%d", sum(got), tc.amount)
			}
		})
	}
}

func TestSplitEquivalentToEqualWeights(t *testing.T) {
	for _, amount := range []int64{0, 1, 7, 100, -7, -100, 1 << 40, -(1 << 40)} {
		for _, n := range []int{1, 2, 3, 7, 16} {
			got, err := Split(amount, n)
			if err != nil {
				t.Fatalf("Split(%d, %d) error: %v", amount, n, err)
			}
			weights := make([]int64, n)
			for i := range weights {
				weights[i] = 1
			}
			want, err := Allocate(amount, weights)
			if err != nil {
				t.Fatalf("Allocate(%d, %v) error: %v", amount, weights, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Split(%d, %d)=%v differs from Allocate=%v", amount, n, got, want)
			}
		}
	}
}

func TestSplitDeterministic(t *testing.T) {
	first, err := Split(-101, 6)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := Split(-101, 6)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("non-deterministic: %v vs %v", got, first)
		}
	}
}
