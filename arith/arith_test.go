package arith

import (
	"testing"

	"ontology/limb"
)

func fullMag(n int) limb.Mag {
	m := make(limb.Mag, n)
	for i := range m {
		m[i] = limb.Base - 1
	}
	return m
}

// 单 limb 乘必须恰好 m 次乘累加（线性下界）；m×m 不得超过 m*m+m。
func TestMulCounter(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		a := Make(1, fullMag(m))
		one := Make(1, limb.Mag{limb.Base - 1})
		Mul(a, one)
		if got := mulOps.Load(); got != int64(m) {
			t.Fatalf("m=%d x1 limb: ops=%d, want %d", m, got, m)
		}
		Mul(a, a)
		if got, hi := mulOps.Load(), int64(m)*int64(m)+int64(m); got > hi {
			t.Fatalf("m=%d x m: ops=%d, want <= %d", m, got, hi)
		}
	}
}
