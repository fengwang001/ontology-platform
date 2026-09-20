package ontology

import (
	"fmt"
	"testing"
)

// Duplicate keys expand to exactly m*n rows per key, and the reported
// OutputRows equals the sum of per-key m*n over thousands of rows.
func TestFanOutExpansionAndCounts(t *testing.T) {
	// Key k gets (k+1) left rows and (k+2) right rows, k in [0,50).
	var left, right []Row
	wantTotal, wantMax := 0, 0
	for k := 0; k < 50; k++ {
		m, n := k+1, k+2
		for i := 0; i < m; i++ {
			left = append(left, Row{"k": k, "li": i})
		}
		for j := 0; j < n; j++ {
			right = append(right, Row{"k": k, "rj": j})
		}
		wantTotal += m * n
		if m*n > wantMax {
			wantMax = m * n
		}
	}

	out, stats, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if stats.OutputRows != wantTotal {
		t.Fatalf("OutputRows = %d, want sum of m*n = %d", stats.OutputRows, wantTotal)
	}
	if len(out) != wantTotal {
		t.Fatalf("len(out) = %d, want %d", len(out), wantTotal)
	}
	if stats.MaxFanOut != wantMax {
		t.Fatalf("MaxFanOut = %d, want %d", stats.MaxFanOut, wantMax)
	}

	// Every (k, li, rj) combination appears exactly once: no dup, no miss.
	seen := make(map[string]int, wantTotal)
	for _, r := range out {
		seen[fmt.Sprintf("%v/%v/%v", r["k"], r["li"], r["rj"])]++
	}
	if len(seen) != wantTotal {
		t.Fatalf("distinct combinations = %d, want %d", len(seen), wantTotal)
	}
	for combo, c := range seen {
		if c != 1 {
			t.Fatalf("combination %s appeared %d times", combo, c)
		}
	}
}

// A focused 3x2 group produces exactly 6 rows and MaxFanOut 6.
func TestFanOutThreeByTwo(t *testing.T) {
	left := []Row{
		{"k": 1, "li": 0},
		{"k": 1, "li": 1},
		{"k": 1, "li": 2},
	}
	right := []Row{
		{"k": 1, "rj": 0},
		{"k": 1, "rj": 1},
	}

	out, stats, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 6 || stats.OutputRows != 6 {
		t.Fatalf("3x2 join produced %d rows, want 6", len(out))
	}
	if stats.MaxFanOut != 6 {
		t.Fatalf("MaxFanOut = %d, want 6", stats.MaxFanOut)
	}
}

// Keys present on only one side contribute nothing to Inner output and do
// not inflate MaxFanOut.
func TestFanOutIgnoresOneSidedKeys(t *testing.T) {
	left := []Row{{"k": 1}, {"k": 2}, {"k": 2}}
	right := []Row{{"k": 2}, {"k": 3}, {"k": 3}, {"k": 3}}

	out, stats, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 2 { // only key 2 matches: 2 left x 1 right
		t.Fatalf("got %d rows, want 2", len(out))
	}
	if stats.MaxFanOut != 2 {
		t.Fatalf("MaxFanOut = %d, want 2", stats.MaxFanOut)
	}
	if stats.LeftNoPartner != 1 {
		t.Fatalf("LeftNoPartner = %d, want 1", stats.LeftNoPartner)
	}
}
