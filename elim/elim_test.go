package elim

import (
	"math"
	"math/rand/v2"
	"testing"
)

// naive 按第一行展开的递归定义（朴素参照）。
func naive(a []float64, n int) float64 {
	if n == 1 {
		return a[0]
	}
	var sum float64
	for j := 0; j < n; j++ {
		sub := make([]float64, 0, (n-1)*(n-1))
		for i := 1; i < n; i++ {
			for c := 0; c < n; c++ {
				if c != j {
					sub = append(sub, a[i*n+c])
				}
			}
		}
		s := 1.0
		if j%2 == 1 {
			s = -1
		}
		sum += s * a[j] * naive(sub, n-1)
	}
	return sum
}

func detOK(t *testing.T, e *Engine, m []float64, n int, want float64) {
	t.Helper()
	if got, err := e.Det(m, n); err != nil || math.Abs(got-want) > 1e-9 {
		t.Errorf("n=%d got %v,%v want %v", n, got, err, want)
	}
}

func TestDetMatchesNaive(t *testing.T) {
	e := New()
	fixed := [][]float64{
		{5},
		{0, 1, 1, 0},                // 一次交换，det=-1
		{0, 1, 1, 1, 0, 1, 1, 1, 0}, // 第三节矩阵，det=2
		{1, 2, 3, 2, 4, 6, 0, 1, 0}, // 奇异
		{2, 0, 0, 0, -3, 0, 0, 0, 4.5},
	}
	for _, m := range fixed {
		n := int(math.Sqrt(float64(len(m))))
		detOK(t, e, m, n, naive(m, n))
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for n := 1; n <= 6; n++ { // 多档规模、随机到达
		for trial := 0; trial < 8; trial++ {
			m := make([]float64, n*n)
			for i := range m {
				m[i] = float64(rng.IntN(11) - 5) // 小整数，含负零正
			}
			detOK(t, e, m, n, naive(m, n))
		}
	}
}

func TestSwapSignParity(t *testing.T) {
	e := New()
	cases := []struct {
		mat  []float64
		n    int
		want float64
	}{
		{[]float64{1, 0, 0, 1}, 2, 1},                // 无交换，符号正
		{[]float64{0, 1, 1, 0}, 2, -1},               // 一次交换，符号负
		{[]float64{0, 1, 0, 0, 0, 1, 1, 0, 0}, 3, 1}, // 三轮换=两次交换，符号正
	}
	for _, c := range cases {
		detOK(t, e, c.mat, c.n, c.want)
	}
}

func TestInputUnmodified(t *testing.T) {
	e := New()
	m := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	before := append([]float64(nil), m...)
	if _, err := e.Det(m, 3); err != nil {
		t.Fatal(err)
	}
	for i := range m {
		if math.Float64bits(m[i]) != math.Float64bits(before[i]) {
			t.Fatalf("input mutated at %d", i)
		}
	}
}
