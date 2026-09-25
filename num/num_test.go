package num

import (
	"math"
	"math/big"
	"testing"
)

// fibPair 返回相邻斐波那契项 (F_k, F_{k-1})，F_k 恰有 digits 位十进制位。
func fibPair(digits int) (*big.Int, *big.Int) {
	a, b := big.NewInt(1), big.NewInt(1)
	threshold := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits-1)), nil)
	for b.Cmp(threshold) < 0 {
		a, b = b, new(big.Int).Add(a, b)
	}
	return b, a
}

// TestGcdLinearInDigits 断言欧几里得迭代步数随位数 m 线性增长。
// 斐波那契相邻项是最坏情形：步数 ≈ 4.785m，Lamé 定理保证 ≤ 5m+5；
// 暴力试除则是 ~10^m 量级。lastSteps 是同包非导出字段，测试直接读取，
// 不经由任何导出的函数或方法。
func TestGcdLinearInDigits(t *testing.T) {
	var prevSteps, prevM int64
	for _, m := range []int{100, 1000, 10000} {
		a, b := fibPair(m)
		if got := GcdBig(a, b); got.Cmp(big.NewInt(1)) != 0 {
			t.Fatalf("m=%d: gcd=%s, want 1", m, got)
		}
		steps := lastSteps.Load()
		if steps < int64(m) || steps > int64(5*m+5) {
			t.Fatalf("m=%d: steps=%d, want in [%d, %d]", m, steps, m, 5*m+5)
		}
		if prevM > 0 {
			ratio := float64(steps) / float64(prevSteps)
			want := float64(m) / float64(prevM)
			if ratio < want/2 || ratio > want*2 {
				t.Fatalf("steps ratio %.2f, want ~%.2f (linear in digits)", ratio, want)
			}
		}
		prevSteps, prevM = steps, int64(m)
	}
}

func TestGcd(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{6, -8, 2}, {-6, 8, 2}, {0, 5, 5}, {5, 0, 5}, {0, 0, 0},
		{-12, 18, 6}, {1, 1, 1}, {math.MaxInt64, 1, 1},
	}
	for _, c := range cases {
		if got := Gcd(c.a, c.b); got != c.want {
			t.Errorf("Gcd(%d,%d)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestOverflowOps(t *testing.T) {
	mul := []struct{ a, b, want int64 }{{3, 4, 12}, {-3, 4, -12}, {0, math.MinInt64, 0}, {math.MinInt64, 1, math.MinInt64}}
	for _, c := range mul {
		if got, err := Mul64(c.a, c.b); err != nil || got != c.want {
			t.Errorf("Mul64(%d,%d)=(%d,%v), want (%d,nil)", c.a, c.b, got, err, c.want)
		}
	}
	mulBad := [][2]int64{{math.MaxInt64, 2}, {math.MinInt64, -1}, {-1, math.MinInt64}, {1 << 62, 2}, {math.MinInt64, 2}}
	for _, c := range mulBad {
		if got, err := Mul64(c[0], c[1]); err != ErrMulOverflow {
			t.Errorf("Mul64(%d,%d)=(%d,%v), want ErrMulOverflow", c[0], c[1], got, err)
		}
	}
	add := []struct{ a, b, want int64 }{{1, 2, 3}, {math.MaxInt64, -1, math.MaxInt64 - 1}, {math.MaxInt64, math.MinInt64, -1}}
	for _, c := range add {
		if got, err := Add64(c.a, c.b); err != nil || got != c.want {
			t.Errorf("Add64(%d,%d)=(%d,%v), want (%d,nil)", c.a, c.b, got, err, c.want)
		}
	}
	addBad := [][2]int64{{math.MaxInt64, 1}, {math.MinInt64, -1}, {1 << 62, 1 << 62}}
	for _, c := range addBad {
		if got, err := Add64(c[0], c[1]); err != ErrAddOverflow {
			t.Errorf("Add64(%d,%d)=(%d,%v), want ErrAddOverflow", c[0], c[1], got, err)
		}
	}
	subBad := [][2]int64{{math.MinInt64, 1}, {math.MaxInt64, -1}}
	for _, c := range subBad {
		if got, err := Sub64(c[0], c[1]); err != ErrAddOverflow {
			t.Errorf("Sub64(%d,%d)=(%d,%v), want ErrAddOverflow", c[0], c[1], got, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		n, d         int64
		wantN, wantD int64
	}{
		{6, -8, -3, 4}, {0, 5, 0, 1}, {0, -7, 0, 1}, {-3, -6, 1, 2},
		{math.MinInt64, 2, math.MinInt64 / 2, 1}, {math.MinInt64, math.MinInt64, 1, 1},
	}
	for _, c := range cases {
		n, d, err := Normalize(c.n, c.d)
		if err != nil || n != c.wantN || d != c.wantD {
			t.Errorf("Normalize(%d,%d)=(%d,%d,%v), want (%d,%d,nil)", c.n, c.d, n, d, err, c.wantN, c.wantD)
		}
	}
	if _, _, err := Normalize(1, 0); err != ErrZeroDenominator {
		t.Errorf("Normalize(1,0) err=%v, want ErrZeroDenominator", err)
	}
	if _, _, err := Normalize(1, math.MinInt64); err != ErrNormOverflow {
		t.Errorf("Normalize(1,MinInt64) err=%v, want ErrNormOverflow", err)
	}
}
