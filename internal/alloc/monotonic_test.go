package alloc

import (
	"math"
	"math/rand"
	"testing"
)

func checkMonotonic(t *testing.T, amount int64, weights, result []int64) {
	t.Helper()
	for i := 0; i < len(weights); i++ {
		for j := i + 1; j < len(weights); j++ {
			// 正金额：权重大的分得不少（result[i] >= result[j]）。
			// 负金额：权重大的承担更多损失，即数值不大于（result[i] <= result[j]）；
			// 若严格按“>=”判定负金额，则与守恒及按比例分摊数学上不可兼得。
			if weights[i] > weights[j] {
				if amount >= 0 && result[i] < result[j] {
					t.Fatalf("amount=%d weights=%v result=%v: w=%d index %d got %d < index %d got %d",
						amount, weights, result, weights[i], i, result[i], j, result[j])
				}
				if amount < 0 && result[i] > result[j] {
					t.Fatalf("amount=%d weights=%v result=%v: w=%d index %d got %d > index %d got %d",
						amount, weights, result, weights[i], i, result[i], j, result[j])
				}
			}
		}
	}
}

func TestMonotonicTable(t *testing.T) {
	cases := []struct {
		amount  int64
		weights []int64
	}{
		{100, []int64{1, 2, 3}},
		{10, []int64{3, 2, 1}},
		{1, []int64{100, 1, 50}},
		{-100, []int64{1, 2, 3}},
		{-10, []int64{3, 2, 1}},
		{-1, []int64{100, 1, 50}},
		{0, []int64{5, 1, 9}},
		{500, []int64{0, 1, 0, 100}},
		{-500, []int64{0, 1, 0, 100}},
		{math.MaxInt64 / 2, []int64{1, 1, 1}},
		{math.MinInt64 / 2, []int64{1, 1, 1}},
	}
	for _, tc := range cases {
		got, err := Allocate(tc.amount, tc.weights)
		if err != nil {
			t.Fatalf("amount=%d weights=%v: %v", tc.amount, tc.weights, err)
		}
		checkMonotonic(t, tc.amount, tc.weights, got)
	}
}

func TestMonotonicRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(12)
		weights := make([]int64, n)
		for i := range weights {
			weights[i] = int64(rng.Intn(1000))
		}
		var amount int64
		if rng.Intn(2) == 0 {
			amount = rng.Int63n(100_000)
		} else {
			amount = -rng.Int63n(100_000)
		}
		got, err := Allocate(amount, weights)
		if err != nil {
			t.Fatalf("iter=%d: %v", iter, err)
		}
		checkMonotonic(t, amount, weights, got)
	}
}
