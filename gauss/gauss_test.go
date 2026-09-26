package gauss

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"

	"ontology/pivot"
)

const eps = 1e-9

func mul(a, b []float64, n int) []float64 {
	c := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := 0; k < n; k++ {
				c[i*n+j] += a[i*n+k] * b[k*n+j]
			}
		}
	}
	return c
}
func deviationFromIdentity(x []float64, n int) (d float64) {
	for i := range x {
		e := math.Abs(x[i])
		if i/n == i%n {
			e = math.Abs(x[i] - 1)
		}
		if e > d {
			d = e
		}
	}
	return d
}
func aug(l, r []float64) string {
	return fmt.Sprintf("[%g %g|%g %g]/[%g %g|%g %g]",
		l[0], l[1], r[0], r[1], l[2], l[3], r[2], r[3])
}

// TestTwoByTwoMatchesDerivation pins the five-row NOTES.md table for A=[[0,2],[1,1]].
func TestTwoByTwoMatchesDerivation(t *testing.T) {
	l, r := []float64{0, 2, 1, 1}, []float64{1, 0, 0, 1}
	got := []string{aug(l, r)}
	if p, ok := pivot.Pick(l, 2, 0); !ok || p != 1 {
		t.Fatalf("k=0 pivot=(%d,%v), want row 1", p, ok)
	}
	for j := 0; j < 2; j++ { // k=0: swap both halves
		l[j], l[2+j], r[j], r[2+j] = l[2+j], l[j], r[2+j], r[j]
	}
	got = append(got, aug(l, r)) // normalize pivot row by 1: unchanged
	s := 1.0 / l[3]              // k=1: normalize row1 by pivot 2
	for j := 0; j < 2; j++ {
		l[2+j], r[2+j] = l[2+j]*s, r[2+j]*s
	}
	got = append(got, aug(l, r))
	m := l[1] // above-elimination: row0 -= 1*row1
	for j := 0; j < 2; j++ {
		l[j], r[j] = l[j]-m*l[2+j], r[j]-m*r[2+j]
	}
	got = append(got, aug(l, r))
	want := []string{"[0 2|1 0]/[1 1|0 1]", "[1 1|0 1]/[0 2|1 0]",
		"[1 1|0 1]/[0 1|0.5 0]", "[1 0|-0.5 1]/[0 1|0.5 0]"}
	if !slices.Equal(got, want) {
		t.Fatalf("augmented snapshots:\n got %v\nwant %v", got, want)
	}
	if d := deviationFromIdentity(l, 2); d > eps {
		t.Fatalf("left half not I: %g", d)
	}
	inv, err := Invert([]float64{0, 2, 1, 1}, 2)
	if err != nil || fmt.Sprint(inv) != "[-0.5 1 0.5 0]" {
		t.Fatalf("Invert=%v err=%v", inv, err)
	}
}

// TestProductIsIdentity: table-driven sizes in random arrival order;
// entries are negative, zero and positive; A*A^-1 == I within 1e-9.
func TestProductIsIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(685))
	for _, idx := range rng.Perm(7) {
		n := []int{1, 2, 3, 4, 7, 12, 25}[idx]
		a := make([]float64, n*n)
		for i := range a {
			a[i] = rng.Float64()*2 - 1
		}
		for i := 0; i < n; i++ { // strict diagonal dominance -> invertible
			a[i*n+i] += float64(n) * (1 + rng.Float64())
		}
		inv, err := Invert(a, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if d := deviationFromIdentity(mul(a, inv, n), n); d > eps {
			t.Fatalf("n=%d A*A^-1 deviates %g", n, d)
		}
	}
}

// TestDiagonalMatricesNeedNoRowOps: counter delta exactly 0 for diagonal
// matrices n = 100, 1000, 10000, in random arrival order.
func TestDiagonalMatricesNeedNoRowOps(t *testing.T) {
	rng := rand.New(rand.NewSource(586))
	for _, idx := range rng.Perm(3) {
		n := []int{100, 1000, 10000}[idx]
		d := make([]float64, n*n)
		for i := 0; i < n; i++ {
			d[i*n+i] = 1
		}
		before := rowOps.Load()
		got, err := Invert(d, n)
		if delta := rowOps.Load() - before; err != nil || delta != 0 {
			t.Fatalf("n=%d delta=%d err=%v", n, delta, err)
		}
		if dev := deviationFromIdentity(got, n); dev > eps {
			t.Fatalf("n=%d inverse of I not I: %g", n, dev)
		}
	}
}

// TestFailuresDoNotAdvanceCounter: three distinct sentinels; no rejected
// call (empty / bad dimension / singular) moves the counter.
func TestFailuresDoNotAdvanceCounter(t *testing.T) {
	cases := []struct {
		a    []float64
		n    int
		want error
	}{
		{nil, 0, ErrEmptyMatrix},
		{[]float64{1, 0, 0, 1}, 3, ErrBadDimension},
		{[]float64{0, 0, 0, 0}, 2, ErrSingular},
		{[]float64{1, 2, 2, 4}, 2, ErrSingular},
	}
	rng := rand.New(rand.NewSource(865))
	for _, idx := range rng.Perm(len(cases)) {
		c := cases[idx]
		before := rowOps.Load()
		if _, err := Invert(c.a, c.n); !errors.Is(err, c.want) {
			t.Fatalf("case %d: err=%v want %v", idx, err, c.want)
		}
		if rowOps.Load() != before {
			t.Fatalf("case %d moved the counter", idx)
		}
	}
	if errors.Is(ErrEmptyMatrix, ErrBadDimension) || errors.Is(ErrBadDimension, ErrSingular) {
		t.Fatal("sentinel errors are not distinct")
	}
}
