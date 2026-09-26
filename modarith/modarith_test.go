package modarith

import "testing"

// naivePow is the deliberately slow reference: multiply by base e times.
func naivePow(base, e, p uint64) uint64 {
	r := uint64(1)
	for i := uint64(0); i < e; i++ {
		r = Mul(r, base, p)
	}
	return r
}

func TestModPowNaive(t *testing.T) {
	cases := []struct{ base, e, p uint64 }{
		{2, 90, 29}, // worked example: expect 6
		{2, 0, 29},
		{2, 1, 29},
		{3, 5, 29},
		{18, 11, 29},
		{7, 1000, 101},
		{5, 65535, 257},
		{123456, 78901, 104729},
		{2, 1<<20 - 1, 97},
	}
	for _, c := range cases {
		got := ModPow(c.base, c.e, c.p)
		want := naivePow(c.base, c.e, c.p)
		if got != want {
			t.Errorf("ModPow(%d,%d,%d)=%d, naive=%d", c.base, c.e, c.p, got, want)
		}
	}
	if got := ModPow(2, 90, 29); got != 6 {
		t.Errorf("ModPow(2,90,29)=%d, want 6", got)
	}
}

func TestMulAdd(t *testing.T) {
	cases := []struct{ a, b, p, wantMul, wantAdd uint64 }{
		{3, 18, 29, 25, 21},
		{28, 28, 29, 1, 27},
		{0, 5, 7, 0, 5},
		{100, 200, 97, (100 * 200) % 97, 300 % 97},
	}
	for _, c := range cases {
		if got := Mul(c.a, c.b, c.p); got != c.wantMul {
			t.Errorf("Mul(%d,%d,%d)=%d, want %d", c.a, c.b, c.p, got, c.wantMul)
		}
		if got := Add(c.a, c.b, c.p); got != c.wantAdd {
			t.Errorf("Add(%d,%d,%d)=%d, want %d", c.a, c.b, c.p, got, c.wantAdd)
		}
	}
}

// TestMulCountSublinear proves square-and-multiply: for e = 2^k - 1
// (k one-bits) the multiplication count stays within 2k plus a constant,
// instead of growing linearly with e like the naive method would.
func TestMulCountSublinear(t *testing.T) {
	const p = 104729
	for _, k := range []int{100, 500, 1000, 5000, 10000} {
		bs := make([]byte, k)
		for i := range bs {
			bs[i] = 1
		}
		modpowBits(2, p, bs)
		got := lastMulCount.Load()
		if limit := uint64(2*k + 8); got > limit {
			t.Errorf("k=%d: %d multiplications, exceeds %d", k, got, limit)
		}
	}
}
