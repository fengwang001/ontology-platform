package pipeline

import (
	"math"
	"testing"
)

func TestBudgetAndRuns200k(t *testing.T) {
	dir := t.TempDir()
	n := 200_000
	// Total rough bytes: each record ~ key(8) + value(8) + 64.
	const perRecord = 8 + 8 + 64
	total := uint64(n * perRecord)
	limit := total / 50
	p, err := Open(Config{Dir: dir, MemoryByte: limit})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		key := keyFixed(i)
		if _, err := p.Ingest(key, []byte("01234567")); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	st := checkOutputStats(t, dir, n)
	if st.PeakResident > st.Limit {
		t.Fatalf("peak %d exceeded limit %d", st.PeakResident, st.Limit)
	}
	if st.Runs < 40 {
		t.Fatalf("runs = %d, want >= 40", st.Runs)
	}
	// Comparison bound 4*N*ceil(log2(K+1)).
	logK := math.Ceil(math.Log2(float64(st.Runs + 1)))
	bound := uint64(4 * float64(n) * logK)
	if st.Comparisons > bound {
		t.Fatalf("comparisons %d > bound %d (K=%d)", st.Comparisons, bound, st.Runs)
	}
}

func keyFixed(i int) string {
	b := []byte("k00000000")
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b)
}
