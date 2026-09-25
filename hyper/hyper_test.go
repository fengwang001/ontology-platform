package hyper

import (
	"errors"
	"math"
	"testing"

	"ontology/vec"
)

func TestVecOps(t *testing.T) {
	cases := []struct {
		name string
		a, b vec.Vector
		dot  float64
		dist float64
	}{
		{"orthogonal", vec.Vector{1, 0}, vec.Vector{0, 1}, 0, math.Sqrt2},
		{"parallel", vec.Vector{1, 2}, vec.Vector{2, 4}, 10, 2.23606797749979},
		{"one-dim", vec.Vector{3}, vec.Vector{-1}, -3, 4},
		{"zero-vector", vec.Vector{0, 0, 0}, vec.Vector{1, 2, 2}, 0, 3},
	}
	for _, c := range cases {
		if got := vec.Dot(c.a, c.b); got != c.dot {
			t.Errorf("%s: Dot = %v, want %v", c.name, got, c.dot)
		}
		if got := vec.Dist(c.a, c.b); math.Abs(got-c.dist) > 1e-12 {
			t.Errorf("%s: Dist = %v, want %v", c.name, got, c.dist)
		}
	}
}

func TestVecCheck(t *testing.T) {
	cases := []struct {
		name string
		v    vec.Vector
		dim  int
		want error
	}{
		{"ok", vec.Vector{1, 2}, 2, nil},
		{"zero-ok", vec.Vector{0, 0}, 2, nil},
		{"dim-mismatch", vec.Vector{1, 2, 3}, 2, vec.ErrDimMismatch},
		{"nan", vec.Vector{1, math.NaN()}, 2, vec.ErrNaN},
		{"pos-inf", vec.Vector{math.Inf(1), 0}, 2, vec.ErrInf},
		{"neg-inf", vec.Vector{math.Inf(-1), 0}, 2, vec.ErrInf},
	}
	for _, c := range cases {
		err := vec.Check(c.v, c.dim)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: Check = %v, want %v", c.name, err, c.want)
		}
		if c.want == vec.ErrDimMismatch {
			de, ok := err.(vec.DimError)
			if !ok || de.Want != 2 || de.Got != 3 {
				t.Errorf("dim error detail = %+v, want {Want:2 Got:3}", err)
			}
		}
	}
}

func TestSignReproducible(t *testing.T) {
	const dim, bits, tables = 8, 12, 4
	vecs := []vec.Vector{
		{1, -2, 3, -4, 5, -6, 7, -8},
		{0, 0, 0, 0, 0, 0, 0, 0}, // zero vector: signature must be all zeros
		{0.5, 0.5, 0.5, 0.5, -0.5, -0.5, -0.5, -0.5},
	}
	fa, fb := New(99, dim, bits, tables), New(99, dim, bits, tables)
	fc := New(100, dim, bits, tables)
	diffSeedDiffers := false
	for i, v := range vecs {
		sa, err := fa.Sign(v)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		sb, _ := fb.Sign(v)
		sc, _ := fc.Sign(v)
		for tb := range sa {
			if sa[tb] != sb[tb] {
				t.Errorf("vec %d table %d: same seed gave %b vs %b", i, tb, sa[tb], sb[tb])
			}
			diffSeedDiffers = diffSeedDiffers || sa[tb] != sc[tb]
		}
		if i == 1 {
			for tb := range sa {
				if sa[tb] != 0 {
					t.Errorf("zero vector table %d: sig = %b, want 0", tb, sa[tb])
				}
			}
		}
	}
	if !diffSeedDiffers {
		t.Error("different seeds produced identical signatures")
	}
	// dim=1 family works.
	f1 := New(1, 1, 4, 2)
	if _, err := f1.Sign(vec.Vector{3}); err != nil {
		t.Errorf("dim=1 Sign: %v", err)
	}
}

func TestHashOpsExact(t *testing.T) {
	cases := []struct{ n, bits, tables int }{
		{1, 4, 1},
		{7, 8, 3},
		{50, 12, 8},
	}
	for _, c := range cases {
		f := New(5, 4, c.bits, c.tables)
		v := vec.Vector{1, 2, 3, 4}
		for i := 0; i < c.n; i++ {
			if _, err := f.Sign(v); err != nil {
				t.Fatal(err)
			}
		}
		if want := int64(c.n * c.bits * c.tables); f.HashOps() != want {
			t.Errorf("n=%d bits=%d tables=%d: HashOps = %d, want %d",
				c.n, c.bits, c.tables, f.HashOps(), want)
		}
	}
}
