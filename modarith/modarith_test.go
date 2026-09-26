package modarith

import (
	"math/big"
	"testing"
)

func naiveRef(base, e, p uint64) uint64 {
	r := uint64(1) % p
	for ; e > 0; e-- {
		r = r * base % p
	}
	return r
}

func TestMulAdd(t *testing.T) {
	cases := []struct{ a, b, p, wantM, wantA uint64 }{
		{3, 5, 29, 15, 8},
		{18, 18, 29, 5, 7},
		{0, 0, 2, 0, 0},
		{27, 28, 29, 2, 26},
		{100, 200, 101, 2, 98},
	}
	for _, c := range cases {
		if got := Mul(c.a, c.b, c.p); got != c.wantM {
			t.Errorf("Mul(%d,%d,%d)=%d, want %d", c.a, c.b, c.p, got, c.wantM)
		}
		if got := Add(c.a, c.b, c.p); got != c.wantA {
			t.Errorf("Add(%d,%d,%d)=%d, want %d", c.a, c.b, c.p, got, c.wantA)
		}
	}
}

func TestModpowNaive(t *testing.T) {
	cases := []struct {
		base, e, p uint64
		want       uint64 // 手算锚点
	}{
		{2, 90, 29, 6},
		{2, 5, 29, 3},
		{2, 11, 29, 18},
		{3, 11, 29, 15},
		{18, 5, 29, 15},
		{7, 0, 13, 1},
		{5, 1, 7, 5},
		{2, 28, 29, 1}, // 费马小定理
		{9, 100, 101, 1},
	}
	for _, c := range cases {
		got := Modpow(c.base, new(big.Int).SetUint64(c.e), c.p)
		if got != c.want {
			t.Errorf("Modpow(%d,%d,%d)=%d, want %d", c.base, c.e, c.p, got, c.want)
		}
		if ref := naiveRef(c.base, c.e, c.p); got != ref {
			t.Errorf("Modpow(%d,%d,%d)=%d, naive=%d", c.base, c.e, c.p, got, ref)
		}
	}
	// 循环生成更多档位与随机私钥式指数
	for p := uint64(2); p < 500; p++ {
		for e := uint64(0); e < 64; e++ {
			base := (p*7 + e) % p
			if got, ref := Modpow(base, new(big.Int).SetUint64(e), p), naiveRef(base, e, p); got != ref {
				t.Fatalf("Modpow(%d,%d,%d)=%d, naive=%d", base, e, p, got, ref)
			}
		}
	}
}

// TestModpowMulCountSublinear 证明平方-乘：e = 2^k - 1（k 位全 1）时
// 模乘次数 <= 2k+常数，不随 e 线性增长。白盒读取非导出计数器。
func TestModpowMulCountSublinear(t *testing.T) {
	one := big.NewInt(1)
	for _, k := range []uint{100, 500, 1000, 5000, 10000} {
		e := new(big.Int).Sub(new(big.Int).Lsh(one, k), one)
		Modpow(2, e, 7919)
		n := lastMulCount.Load()
		if limit := uint64(2*k + 8); n > limit {
			t.Errorf("k=%d: mul count %d > %d (grows with e, not k)", k, n, limit)
		}
	}
	// 全 1 指数：乘法次数恒为 k 次平方 + k 次乘 = 2k，与 e 的量级无关
	Modpow(2, new(big.Int).Sub(new(big.Int).Lsh(one, 100), one), 7919)
	if n := lastMulCount.Load(); n != 200 {
		t.Errorf("k=100: mul count = %d, want exactly 2k = 200", n)
	}
}
