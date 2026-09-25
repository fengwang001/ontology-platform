package pow

import (
	"math/big"
	"math/bits"
	"sync"
	"testing"

	"ontology/mod"
)

// 不变量 1：与 big 精确参照逐条一致（exp ≤ 12）。
func TestPowModNaiveReference(t *testing.T) {
	cases := []struct{ b, e, m int64 }{
		{-2, 3, 5}, {-3, 2, 7}, {-3, 3, 7}, {7, 12, 13},
		{-5, 3, 9}, {2, 0, 5}, {5, 0, 1}, {2, 10, 1},
		{0, 0, 7}, {0, 5, 7}, {-1, 11, 3}, {123456789, 12, 1000000007},
		{-987654321, 12, 97}, {1 << 40, 7, 1<<31 - 1},
	}
	for _, c := range cases {
		want := new(big.Int).Mod(
			new(big.Int).Exp(big.NewInt(c.b), big.NewInt(c.e), nil),
			big.NewInt(c.m)).Int64()
		got, err := PowMod(c.b, c.e, c.m)
		if err != nil || got != want {
			t.Errorf("PowMod(%d,%d,%d)=%d,%v want %d", c.b, c.e, c.m, got, err, want)
		}
	}
}

// 不变量 2：结果恒在 [0, mod)；负 base 等价于归一化后再幂；mod=1 恒 0。
func TestModSemantics(t *testing.T) {
	cases := []struct{ b, e, m int64 }{
		{-2, 3, 5}, {-3, 3, 7}, {-5, 3, 9}, {7, 100, 13},
		{5, 0, 1}, {2, 10, 1}, {-1, 0, 2}, {math64Min, 3, 1<<31 - 1},
	}
	for _, c := range cases {
		got, err := PowMod(c.b, c.e, c.m)
		if err != nil {
			t.Fatalf("PowMod(%d,%d,%d) err %v", c.b, c.e, c.m, err)
		}
		if got < 0 || got >= c.m {
			t.Errorf("PowMod(%d,%d,%d)=%d 越出 [0,mod)", c.b, c.e, c.m, got)
		}
		ref, _ := PowMod(mod.Normalize(c.b, c.m), c.e, c.m)
		if ref != got {
			t.Errorf("负 base 不等价: PowMod(%d)=%d PowMod(Normalize)=%d", c.b, got, ref)
		}
		if c.m == 1 && got != 0 {
			t.Errorf("mod=1 应恒 0, got %d", got)
		}
	}
}

const math64Min = -1 << 63

// 不变量 3：指数拆分 PowMod(e1+e2) = MulMod(PowMod(e1), PowMod(e2))。
func TestExponentSplit(t *testing.T) {
	cases := []struct{ b, e1, e2, m int64 }{
		{-2, 1, 2, 5}, {7, 40, 60, 13}, {-5, 0, 3, 9},
		{123456789, 500, 487, 1000000007}, {3, 0, 0, 7},
	}
	for _, c := range cases {
		full, _ := PowMod(c.b, c.e1+c.e2, c.m)
		p1, _ := PowMod(c.b, c.e1, c.m)
		p2, _ := PowMod(c.b, c.e2, c.m)
		if mod.MulMod(p1, p2, c.m) != full {
			t.Errorf("拆分失败 b=%d e1=%d e2=%d m=%d", c.b, c.e1, c.e2, c.m)
		}
	}
}

// 不变量 4：三类哨兵错误互不相同，不 panic、不返回半成品。
func TestErrorSentinels(t *testing.T) {
	if ErrZeroModulus == ErrNegativeModulus ||
		ErrNegativeModulus == ErrNegativeExponent ||
		ErrZeroModulus == ErrNegativeExponent {
		t.Fatal("哨兵错误必须互不相同")
	}
	cases := []struct {
		b, e, m int64
		want    error
	}{
		{1, 1, 0, ErrZeroModulus},
		{1, 1, -1, ErrNegativeModulus},
		{1, 1, -1 << 62, ErrNegativeModulus},
		{1, -1, 5, ErrNegativeExponent},
		{0, 0, 0, ErrZeroModulus},
	}
	for _, c := range cases {
		got, err := PowMod(c.b, c.e, c.m)
		if err != c.want || got != 0 {
			t.Errorf("PowMod(%d,%d,%d)=(%d,%v) want (0,%v)", c.b, c.e, c.m, got, err, c.want)
		}
	}
}

// 复杂度：mulmod 次数 ≤ 2*ceil(log2(exp)) + 1，随 exp 对数增长。
func TestMulModCountLogarithmic(t *testing.T) {
	for exp := int64(100); exp <= 10000; exp += 99 {
		if _, err := PowMod(3, exp, 1000000007); err != nil {
			t.Fatal(err)
		}
		ceil := int64(bits.Len64(uint64(exp - 1))) // ceil(log2(exp))，exp>1
		if got := lastMulModCalls.Load(); got > 2*ceil+1 {
			t.Errorf("exp=%d mulmod=%d 超过上界 %d", exp, got, 2*ceil+1)
		}
	}
}

// 并发：N 个 goroutine 对同一批三元组调用，结果与串行逐条相同。
func TestPowModConcurrent(t *testing.T) {
	triples := [][3]int64{{-2, 3, 5}, {7, 100, 13}, {5, 0, 1}, {123456789, 987, 1000000007}}
	serial := make([]int64, len(triples))
	for i, tr := range triples {
		serial[i], _ = PowMod(tr[0], tr[1], tr[2])
	}
	const n = 32
	parallel := make([][]int64, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		parallel[g] = make([]int64, len(triples))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, tr := range triples {
				parallel[g][i], _ = PowMod(tr[0], tr[1], tr[2])
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		for i := range serial {
			if parallel[g][i] != serial[i] {
				t.Errorf("goroutine %d 第 %d 条: %d != %d", g, i, parallel[g][i], serial[i])
			}
		}
	}
}
