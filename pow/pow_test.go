package pow

import (
	"math/big"
	"math/bits"
	"sync"
	"testing"

	"ontology/mod"
)

var cases = []struct{ b, e, m int64 }{
	{-2, 3, 5}, {-3, 2, 7}, {-3, 3, 7}, {7, 100, 13},
	{-5, 3, 9}, {2, 0, 5}, {5, 0, 1}, {2, 10, 1},
	{0, 0, 7}, {0, 5, 7}, {-1, 0, 2}, {9223372036854775806, 3, 9223372036854775807},
}

func naiveBig(b, e, m int64) int64 {
	r := new(big.Int).Mod(new(big.Int).Exp(big.NewInt(b), big.NewInt(e), nil), big.NewInt(m))
	return r.Int64()
}

func TestAgainstNaiveBig(t *testing.T) {
	for b := int64(-9); b <= 9; b++ {
		for e := int64(0); e <= 12; e++ {
			for m := int64(1); m <= 23; m++ {
				got, err := PowMod(b, e, m)
				if err != nil || got != naiveBig(b, e, m) {
					t.Fatalf("PowMod(%d,%d,%d)=%v,%v want %d", b, e, m, got, err, naiveBig(b, e, m))
				}
			}
		}
	}
}

func TestModSemantics(t *testing.T) {
	for _, c := range cases {
		got, err := PowMod(c.b, c.e, c.m)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got < 0 || got >= c.m {
			t.Fatalf("PowMod%v=%d out of [0,%d)", c, got, c.m)
		}
		norm, _ := PowMod(mod.Normalize(c.b, c.m), c.e, c.m)
		if got != norm {
			t.Fatalf("negative base not normalized: %d != %d for %v", got, norm, c)
		}
	}
}

func TestExponentSplit(t *testing.T) {
	for _, c := range cases {
		for _, e1 := range []int64{0, 1, c.e / 2, c.e} {
			if e1 > c.e {
				continue
			}
			e2 := c.e - e1
			p1, _ := PowMod(c.b, e1, c.m)
			p2, _ := PowMod(c.b, e2, c.m)
			full, _ := PowMod(c.b, c.e, c.m)
			if mod.Mulmod(p1, p2, c.m) != full {
				t.Fatalf("split %d=%d+%d failed for %v", c.e, e1, e2, c)
			}
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	got0, e0 := PowMod(3, 2, 0)
	gotN, eN := PowMod(3, 2, -7)
	gotX, eX := PowMod(3, -1, 7)
	if e0 != ErrModZero || eN != ErrModNegative || eX != ErrExpNegative {
		t.Fatalf("wrong sentinels: %v %v %v", e0, eN, eX)
	}
	if e0 == eN || e0 == eX || eN == eX {
		t.Fatal("sentinels not mutually distinct")
	}
	if got0 != 0 || gotN != 0 || gotX != 0 {
		t.Fatal("rejected call returned a partial result")
	}
}

func TestMulmodCountLogarithmic(t *testing.T) {
	for e := int64(100); e <= 10000; e = e*3/2 + 1 {
		if _, err := PowMod(7, e, 1000000007); err != nil {
			t.Fatal(err)
		}
		limit := int64(2*bits.Len64(uint64(e-1))) + 1 // 2*ceil(log2(e))+1
		if n := lastMulmods.Load(); n > limit {
			t.Fatalf("exp=%d used %d mulmods, limit %d", e, n, limit)
		}
	}
}

func TestMulmodNoOverflow(t *testing.T) {
	mm := []struct{ a, b, m, want int64 }{
		{4294967296, 4294967296, 1000000007, 582344008},
		{9223372036854775806, 9223372036854775806, 9223372036854775807, 1},
		{0, 5, 7, 0},
		{6, 6, 1, 0},
	}
	for _, c := range mm {
		if got := mod.Mulmod(c.a, c.b, c.m); got != c.want {
			t.Fatalf("Mulmod%v=%d want %d", c, got, c.want)
		}
	}
}

func TestConcurrent(t *testing.T) {
	serial := make([]int64, len(cases))
	for i, c := range cases {
		serial[i], _ = PowMod(c.b, c.e, c.m)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, c := range cases {
				got, err := PowMod(c.b, c.e, c.m)
				if err != nil || got != serial[i] {
					t.Errorf("concurrent mismatch on %v", c)
				}
			}
		}()
	}
	wg.Wait()
}
