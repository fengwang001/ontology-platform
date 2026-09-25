package lf

import (
	"math/rand"
	"testing"

	"ontology/rot"
)

// TestLFRankCheckConstant: the number of characters examined by a single
// LF lookup must stay below a small constant independent of m, proving
// rank is answered from the count tables, not by scanning last.
func TestLFRankCheckConstant(t *testing.T) {
	const limit = 4
	rng := rand.New(rand.NewSource(7))
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := make([]byte, m)
		for i := range s {
			s[i] = byte(rng.Intn(255)) + 1 // never the terminator 0x00
		}
		last, _, err := rot.Transform(s, 0x00)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		tab, err := NewTable(last, 0x00)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for _, i := range []int{0, 1, m / 3, m / 2, m - 1, m} {
			tab.LF(i)
			if tab.checked > limit {
				t.Fatalf("m=%d i=%d: LF examined %d chars, want <= %d", m, i, tab.checked, limit)
			}
		}
	}
}

// TestLFAgainstNaive checks Table.LF against the LF definition computed
// by linear scans over last.
func TestLFAgainstNaive(t *testing.T) {
	last, _, err := rot.Transform([]byte("mississippi"), 0x00)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := NewTable(last, 0x00)
	if err != nil {
		t.Fatal(err)
	}
	for i := range last {
		less, rank := 0, 0
		for j := range last {
			if last[j] < last[i] {
				less++
			}
			if j <= i && last[j] == last[i] {
				rank++
			}
		}
		if got, want := tab.LF(i), less+rank-1; got != want {
			t.Fatalf("LF[%d]=%d, want %d", i, got, want)
		}
	}
}

// TestNewTableRejectsBadTerminatorCount pins ErrTerminatorCount.
func TestNewTableRejectsBadTerminatorCount(t *testing.T) {
	for _, last := range [][]byte{[]byte("annb$aa$"), []byte("annbaa"), []byte("")} {
		if _, err := NewTable(last, '$'); err != ErrTerminatorCount {
			t.Fatalf("last=%q: got %v, want ErrTerminatorCount", last, err)
		}
	}
}
