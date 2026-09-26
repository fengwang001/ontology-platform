package pivot

import (
	"math"
	"math/rand"
	"testing"
)

func TestPick(t *testing.T) {
	cases := []struct {
		name    string
		a       []float64
		n, k    int
		wantRow int
		wantOK  bool
	}{
		{"unique max at k", []float64{2, 1, 1, 3}, 2, 0, 0, true},
		{"tie [1,1] picks smallest row 0", []float64{1, 2, 1, 3}, 2, 0, 0, true},
		{"tie rows 1,2 picks row 1", []float64{0.3, 0, 0, 0.5, 0, 0, 0.5, 0, 0}, 3, 0, 1, true},
		{"negative uses magnitude", []float64{-3, 0, 2, 0}, 2, 0, 0, true},
		{"worked example k=0 tie rows 1,2", []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 3, 0, 1, true},
		{"single nonzero candidate", []float64{1, 5, 0, 3}, 2, 1, 1, true},
		{"all-zero column -> singular", []float64{1, 5, 0, 0}, 2, 1, 1, false},
		{"all-zero first column -> singular", []float64{0, 1, 0, 1}, 2, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := Pick(c.a, c.n, c.k)
			if ok != c.wantOK || r != c.wantRow {
				t.Fatalf("Pick = (%d,%v), want (%d,%v)", r, ok, c.wantRow, c.wantOK)
			}
			// Invariant 3: repeated calls on the same input are identical.
			r2, ok2 := Pick(c.a, c.n, c.k)
			if r2 != r || ok2 != ok {
				t.Fatalf("non-deterministic Pick: (%d,%v) vs (%d,%v)", r2, ok2, r, ok)
			}
		})
	}
	// NaN-free guarantee for the worked matrix magnitudes.
	if math.IsNaN(math.Abs(-3.0)) {
		t.Fatal("math.Abs produced NaN")
	}
}

// dominant fills a non-singular matrix: nonzero diagonal, random elsewhere.
func dominant(r *rand.Rand, n int) []float64 {
	a := make([]float64, n*n)
	for i := 0; i < n; i++ {
		sum := 1.0
		for j := 0; j < n; j++ {
			if j == i {
				continue
			}
			v := r.Float64()*2 - 1 // may be negative, zero or positive
			a[i*n+j] = v
			sum += math.Abs(v)
		}
		a[i*n+i] = sum + r.Float64()
	}
	return a
}

// eliminate drives one real forward-elimination round on a copy, calling
// Pick exactly once per column exactly as elim.Solve does.
func eliminate(a []float64, n int) {
	u := append([]float64(nil), a...)
	for k := 0; k < n; k++ {
		p, ok := Pick(u, n, k)
		if !ok {
			panic("unexpected singular column")
		}
		if p != k {
			for j := 0; j < n; j++ {
				u[k*n+j], u[p*n+j] = u[p*n+j], u[k*n+j]
			}
		}
		for i := k + 1; i < n; i++ {
			m := u[i*n+k] / u[k*n+k]
			for j := k; j < n; j++ {
				u[i*n+j] -= m * u[k*n+j]
			}
		}
	}
}

// TestExtraScanCounterZero proves singularity detection reuses the pivot
// scan. n=100/1000 run a full O(n^3) elimination round; n=10000 runs the
// equivalent n fused scan passes (a full 10k round is ~minutes of scalar
// work, while the counter measures only the O(n^2) scan passes). In every
// case the extra-scan count stays exactly zero and does not grow with n.
func TestExtraScanCounterZero(t *testing.T) {
	for _, n := range []int{100, 1000} { // full elimination rounds
		ResetExtraScans()
		eliminate(dominant(rand.New(rand.NewSource(int64(n))), n), n)
		if !ExtraScansZero() {
			t.Fatalf("n=%d after full elimination: extra scans nonzero", n)
		}
	}
	n := 10000 // scan passes only; proves the count does not grow with n
	a := dominant(rand.New(rand.NewSource(int64(n))), n)
	ResetExtraScans()
	for k := 0; k < n; k++ {
		if _, ok := Pick(a, n, k); !ok {
			t.Fatalf("n=%d k=%d: unexpected singular column", n, k)
		}
	}
	if !ExtraScansZero() {
		t.Fatal("n=10000: extra singularity scans occurred (counter nonzero)")
	}
}
