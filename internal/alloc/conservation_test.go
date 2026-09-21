package alloc

import (
	"math"
	"math/rand"
	"testing"
)

func TestConservationTable(t *testing.T) {
	cases := []struct {
		amount  int64
		weights []int64
	}{
		{100, []int64{1, 1, 1}},
		{1, []int64{1, 1, 1}},
		{0, []int64{5, 0, 3}},
		{-1, []int64{1, 1, 1}},
		{-100, []int64{1, 2, 3}},
		{math.MaxInt64, []int64{1, 1}},
		{math.MinInt64, []int64{1, 1, 1, 1}},
	}
	for _, tc := range cases {
		got, err := Allocate(tc.amount, tc.weights)
		if err != nil {
			t.Fatalf("amount=%d weights=%v: %v", tc.amount, tc.weights, err)
		}
		var sum int64
		for _, v := range got {
			sum += v
		}
		if sum != tc.amount {
			t.Fatalf("amount=%d weights=%v: sum=%d", tc.amount, tc.weights, sum)
		}
	}
}

// TestConservationRandom 随机化守恒测试：随机 amount（含负数与极端值）
// 与随机权重，反复验证 sum(result) == amount 精确成立。
func TestConservationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20260921))
	const iterations = 5000

	extreme := []int64{
		0, 1, -1, 2, -2,
		math.MaxInt64, math.MinInt64,
		math.MaxInt64 - 1, math.MinInt64 + 1,
	}

	for iter := 0; iter < iterations; iter++ {
		var amount int64
		switch rng.Intn(5) {
		case 0:
			amount = extreme[rng.Intn(len(extreme))]
		case 1:
			amount = -rng.Int63n(1_000_000)
		case 2:
			amount = rng.Int63n(1_000_000)
		default:
			amount = rng.Int63() - rng.Int63()
		}

		n := 1 + rng.Intn(16)
		weights := make([]int64, n)
		for i := range weights {
			switch rng.Intn(4) {
			case 0:
				weights[i] = 0
			case 1:
				weights[i] = 1 + rng.Int63n(10)
			case 2:
				weights[i] = int64(1 + rng.Intn(1_000))
			default:
				weights[i] = rng.Int63n(1 << 40)
			}
		}

		got, err := Allocate(amount, weights)
		if err != nil {
			// 随机组合可能恰好全 0 权重或触发溢出，均为合法错误路径。
			if err != ErrOverflow && err != ErrZeroTotalWeight {
				t.Fatalf("iter=%d amount=%d weights=%v: unexpected error %v",
					iter, amount, weights, err)
			}
			continue
		}

		var sum int64
		for _, v := range got {
			sum += v
		}
		if sum != amount {
			t.Fatalf("iter=%d amount=%d weights=%v: sum=%d",
				iter, amount, weights, sum)
		}
	}
}
