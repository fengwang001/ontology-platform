package sar

import (
	"errors"
	"testing"
)

func TestNewWidth(t *testing.T) {
	ok := []int{1, 2, 3, 4, 7, 16, 31, 63}
	bad := []int{0, -1, -8, 64, 100, 1 << 30}
	for _, n := range ok {
		a, err := New(n)
		if err != nil || a.Width() != n || a.Mod() != uint64(1)<<uint(n) ||
			a.Half() != uint64(1)<<uint(n-1) || a.Mask() != a.Mod()-1 {
			t.Fatalf("New(%d) = %+v, %v", n, a, err)
		}
	}
	for _, n := range bad {
		if _, err := New(n); !errors.Is(err, ErrWidth) {
			t.Fatalf("New(%d) err = %v, want ErrWidth", n, err)
		}
	}
}

func TestValidAndDiff(t *testing.T) {
	cases := []struct {
		n       int
		x, y, d uint64
	}{
		{1, 0, 1, 1},
		{1, 1, 0, 1},
		{4, 15, 0, 1}, // wraps forward by one
		{4, 0, 15, 15},
		{4, 2, 10, 8}, // exactly half
		{4, 10, 2, 8},
		{8, 200, 56, 112},
	}
	for _, c := range cases {
		a, _ := New(c.n)
		if !a.Valid(c.x) || a.Diff(c.x, c.y) != c.d {
			t.Fatalf("n=%d Diff(%d,%d)=%d want %d", c.n, c.x, c.y, a.Diff(c.x, c.y), c.d)
		}
		if err := a.CheckValid(c.x); err != nil {
			t.Fatalf("CheckValid(%d): %v", c.x, err)
		}
	}
	a, _ := New(4)
	for _, s := range []uint64{16, 17, 1 << 20} {
		if a.Valid(s) || !errors.Is(a.CheckValid(s), ErrOutOfRange) {
			t.Fatalf("Valid(%d) must be false with ErrOutOfRange", s)
		}
	}
}

func TestCmpTable(t *testing.T) {
	cases := []struct {
		n    int
		x, y uint64
		want Rel
	}{
		{4, 5, 5, Equal},
		{4, 0, 7, Less},
		{4, 15, 0, Less},         // wrap: 15 is immediately before 0
		{4, 13, 0, Less},         // section 3 chain
		{4, 2, 10, Incomparable}, // d == 8 half circle
		{4, 10, 2, Incomparable},
		{4, 0, 8, Incomparable},
		{4, 0, 9, Greater},
		{4, 1, 0, Greater},      // forward distance 15
		{1, 0, 1, Incomparable}, // M/2 == 1 for N=1
		{2, 0, 1, Less},
		{2, 0, 2, Incomparable},
		{63, 0, uint64(1) << 62, Incomparable},
	}
	for _, c := range cases {
		a, _ := New(c.n)
		if got := a.Cmp(c.x, c.y); got != c.want {
			t.Fatalf("n=%d Cmp(%d,%d) = %s, want %s", c.n, c.x, c.y, got, c.want)
		}
		if a.Classify(a.Diff(c.x, c.y)) != c.want {
			t.Fatalf("n=%d Classify(Diff(%d,%d)) mismatch", c.n, c.x, c.y)
		}
	}
}

func TestCmpAntisymmetry(t *testing.T) {
	for n := 1; n <= 10; n++ {
		a, _ := New(n)
		m := a.Mod()
		for x := uint64(0); x < m; x++ {
			for y := uint64(0); y < m; y++ {
				r1, r2 := a.Cmp(x, y), a.Cmp(y, x)
				switch {
				case x == y:
					if r1 != Equal || r2 != Equal {
						t.Fatalf("n=%d Cmp(%d,%d)=%s, rev=%s", n, x, y, r1, r2)
					}
				case r1 == Incomparable:
					if r2 != Incomparable {
						t.Fatalf("n=%d half must be symmetric: %d,%d -> %s,%s", n, x, y, r1, r2)
					}
				case r1 == Less:
					if r2 != Greater {
						t.Fatalf("n=%d antisymmetry broken: %d,%d -> %s,%s", n, x, y, r1, r2)
					}
				default:
					if r1 != Greater || r2 != Less {
						t.Fatalf("n=%d antisymmetry broken: %d,%d -> %s,%s", n, x, y, r1, r2)
					}
				}
			}
		}
	}
}

func TestRelString(t *testing.T) {
	want := map[Rel]string{Equal: "Equal", Less: "Less", Incomparable: "Incomparable", Greater: "Greater"}
	for r, s := range want {
		if r.String() != s {
			t.Fatalf("%d.String() = %q, want %q", r, r.String(), s)
		}
	}
}
