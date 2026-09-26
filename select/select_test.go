package selectx

import (
	"errors"
	"math"
	"testing"

	"ontology/samp"
)

// TestSampleAccessCountEqualsS proves the complexity bound: for fixed s, one
// Sample visits exactly s population elements regardless of N. It reads the
// unexported counter directly (same package) — no exported API exposes it.
func TestSampleAccessCountEqualsS(t *testing.T) {
	const s = 10
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		plan, err := samp.New(n, s, 0)
		if err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
		sel := New(plan)
		pop := make([]int64, n)
		for i := range pop {
			pop[i] = int64(i)
		}
		out, err := sel.Sample(pop)
		if err != nil {
			t.Fatalf("N=%d: unexpected error %v", n, err)
		}
		if sel.accessed != s {
			t.Errorf("N=%d: accessed %d elements, want exactly %d", n, sel.accessed, s)
		}
		if sel.accessed == n {
			t.Errorf("N=%d: accessed all N elements (scan), want direct indexing", n)
		}
		if len(out) != s {
			t.Errorf("N=%d: got %d values, want %d", n, len(out), s)
		}
	}
}

// TestSelectSampleValues is table-driven: fetched values must equal the
// population entries at floor(r + i*N/s).
func TestSelectSampleValues(t *testing.T) {
	cases := []struct {
		n, s int
		r    float64
	}{
		{10, 4, 1.0},
		{10, 1, 0},
		{10, 10, 0},
		{7, 3, 0.5},
		{100, 10, 0},
	}
	for _, c := range cases {
		plan, err := samp.New(c.n, c.s, c.r)
		if err != nil {
			t.Fatalf("n=%d s=%d r=%v: %v", c.n, c.s, c.r, err)
		}
		pop := make([]int64, c.n)
		for i := range pop {
			pop[i] = int64(i * 2)
		}
		got, err := New(plan).Sample(pop)
		if err != nil {
			t.Fatalf("n=%d s=%d r=%v: %v", c.n, c.s, c.r, err)
		}
		d := float64(c.n) / float64(c.s)
		for i := 0; i < c.s; i++ {
			at := int(math.Floor(c.r + float64(i)*d))
			if got[i] != pop[at] {
				t.Errorf("n=%d s=%d r=%v i=%d: got %d want pop[%d]=%d",
					c.n, c.s, c.r, i, got[i], at, pop[at])
			}
		}
	}
}

// TestRejectedSampleLeavesCounter proves a length mismatch changes no state:
// it returns the sentinel error and the counter is not reset or mutated.
func TestRejectedSampleLeavesCounter(t *testing.T) {
	plan, err := samp.New(10, 4, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	sel := New(plan)
	pop := []int64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if _, err := sel.Sample(pop); err != nil {
		t.Fatal(err)
	}
	before := sel.accessed
	_, err = sel.Sample(make([]int64, 9))
	if !errors.Is(err, ErrPopulationLengthMismatch) {
		t.Fatalf("got %v, want ErrPopulationLengthMismatch", err)
	}
	if sel.accessed != before {
		t.Errorf("counter changed after rejection: before=%d after=%d", before, sel.accessed)
	}
}
