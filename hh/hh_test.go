package hh

import (
	"math"
	"testing"
)

const eps = 1e-12

func reflect(v, x []float64) []float64 { // H x for normalized v
	var vv, vx float64
	for i := range v {
		vv += v[i] * v[i]
		vx += v[i] * x[i]
	}
	tau := 2 * vx / vv
	y := make([]float64, len(x))
	for i := range x {
		y[i] = x[i] - tau*v[i]
	}
	return y
}

func TestReflector(t *testing.T) {
	cases := []struct {
		name     string
		x        []float64
		wantOK   bool
		wantDiag float64 // sign(x0)*||x||
		exactV   []float64
	}{
		{"notes", []float64{3, 4}, true, 5, []float64{1, -2}},
		{"sign zero is plus", []float64{0, 1}, true, 1, []float64{1, -1}},
		{"negative first", []float64{-3, 4}, true, -5, []float64{1, 2}},
		{"tail already zero", []float64{5, 0, 0}, false, 5, nil},
		{"single element", []float64{7}, false, 7, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, ok := Reflector(c.x)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v", ok, c.wantOK)
			}
			if !ok {
				if v != nil {
					t.Fatalf("skipped column must not build v, got %v", v)
				}
				return
			}
			if v[0] != 1 {
				t.Fatalf("v[0]=%v, must be 1", v[0])
			}
			for i, want := range c.exactV {
				if math.Abs(v[i]-want) > eps {
					t.Fatalf("v[%d]=%v want %v (v=%v)", i, v[i], want, v)
				}
			}
			y := reflect(v, c.x)
			if math.Abs(y[0]-c.wantDiag) > eps {
				t.Fatalf("diagonal=%v want %v", y[0], c.wantDiag)
			}
			for i := 1; i < len(y); i++ {
				if math.Abs(y[i]) > eps {
					t.Fatalf("lower part not zeroed: %v", y)
				}
			}
		})
	}
}

func TestApply(t *testing.T) {
	// A = [[3,1],[4,1]]: one reflector on columns 0..1.
	a := []float64{3, 1, 4, 1}
	v, ok := Reflector([]float64{3, 4})
	if !ok {
		t.Fatal("expected reflector")
	}
	Apply(v, a, 2, 0)
	want := []float64{5, 7.0 / 5, 0, 1.0 / 5}
	for i := range want {
		if math.Abs(a[i]-want[i]) > eps {
			t.Fatalf("R=%v want %v", a, want)
		}
	}
}

func TestApplyPreservesEarlierColumns(t *testing.T) {
	for _, c := range []struct{ n, k int }{{3, 1}, {4, 1}, {4, 2}, {7, 3}, {9, 5}} {
		a := make([]float64, c.n*c.n)
		for i := range a {
			a[i] = float64((i*7+3)%11) - 5
		}
		before := append([]float64(nil), a...)
		tail := make([]float64, c.n-c.k)
		for i := range tail {
			tail[i] = a[(c.k+i)*c.n+c.k]
		}
		v, ok := Reflector(tail)
		if !ok {
			t.Fatalf("n=%d k=%d: expected a reflector", c.n, c.k)
		}
		Apply(v, a, c.n, c.k)
		for i := 0; i < c.n; i++ {
			for j := 0; j < c.k; j++ {
				if a[i*c.n+j] != before[i*c.n+j] {
					t.Fatalf("n=%d k=%d: column %d rewritten", c.n, c.k, j)
				}
			}
		}
	}
}

func TestApplyOnlyTouchesTail(t *testing.T) {
	// 4x4, k=2: rows/cols 0..1 and rows <2 must be byte-identical afterwards.
	x := []float64{
		2, -1, 0, 3,
		1, 0, 1, -2,
		0, 4, 2, 1,
		-3, 1, 2, 1,
	}
	before := append([]float64(nil), x...)
	v, _ := Reflector([]float64{2, 1})
	Apply(v, x, 4, 2)
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			idx := i*4 + j
			untouched := j < 2 || i < 2
			if untouched && x[idx] != before[idx] {
				t.Fatalf("element (%d,%d) rewritten", i, j)
			}
		}
	}
}
