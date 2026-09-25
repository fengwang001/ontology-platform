package aln

import (
	"math/rand"
	"testing"
)

// TestRunningMinComparisons pins the incremental running minimum: feeding
// m strictly decreasing TS in one batch must cost exactly m comparisons
// (one per event against the running min), never the O(m^2) rescan cost.
func TestRunningMinComparisons(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		a := New()
		a.Begin()
		for i := m; i > 0; i-- {
			if !a.Accept(int64(i)) {
				t.Fatalf("m=%d: event %d unexpectedly rejected", m, i)
			}
		}
		a.Close()
		if a.cmps != m {
			t.Fatalf("m=%d: cmps=%d, want exactly %d (incremental), not %d (rescan)",
				m, a.cmps, m, m*(m-1)/2)
		}
		if got := a.Aligned()[0]; got != 1 {
			t.Fatalf("m=%d: aligned=%d want 1", m, got)
		}
	}
}

func TestAlignerTable(t *testing.T) {
	cases := []struct {
		name   string
		feeds  [][]int64 // one inner slice per Begin..Close
		want   []int64
		counts []int // accepted per batch
	}{
		{"single batch unordered", [][]int64{{10, 5}}, []int64{5}, []int{2}},
		{"empty batch0 is no lower bound", [][]int64{{}, {20, 12}}, []int64{None, 12}, []int{0, 2}},
		{"empty middle batch carries forward", [][]int64{{5}, {}, {100}}, []int64{5, 5, 100}, []int{1, 0, 1}},
		{"late rejected, strict less-than", [][]int64{{5}, {8, 12, 4}, {100}}, []int64{5, 8, 100}, []int{1, 2, 1}},
		{"equal to prev is on time", [][]int64{{12}, {12}, {8, 13}}, []int64{12, 12, 13}, []int{1, 1, 1}},
		{"only leading empties", [][]int64{{}, {}}, []int64{None, None}, []int{0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := New()
			for i, ts := range c.feeds {
				a.Begin()
				if !a.Open() {
					t.Fatal("Open false after Begin")
				}
				for _, x := range ts {
					want := a.prev == None || x >= a.prev // independent lateness check
					if got := a.Accept(x); got != want {
						t.Fatalf("batch %d ts=%d Accept=%v want %v", i, x, got, want)
					}
				}
				if a.Count() != c.counts[i] {
					t.Fatalf("batch %d Count=%d want %d", i, a.Count(), c.counts[i])
				}
				a.Close()
			}
			got := a.Aligned()
			if len(got) != len(c.want) {
				t.Fatalf("Aligned len=%d want %d (%v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("A(%d)=%d want %d; full %v", i, got[i], c.want[i], got)
				}
			}
		})
	}
}

// TestAlignerRandomMonotonic drives random streams and independently
// recomputes aligned times, checking the non-decreasing suffix once a
// finite lower bound exists (leading None entries are allowed).
func TestAlignerRandomMonotonic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		a := New()
		var refPrev int64 = None
		var ref []int64
		for b := 0; b < 6; b++ {
			var m int64 = None
			n := 0
			a.Begin()
			for k := rng.Intn(8); k > 0; k-- {
				ts := rng.Int63n(40)
				if refPrev == None || ts >= refPrev {
					a.Accept(ts)
					n++
					if ts < m {
						m = ts
					}
				} else if a.Accept(ts) {
					t.Fatal("late event accepted")
				}
			}
			a.Close()
			if n > 0 {
				refPrev = m
			}
			ref = append(ref, refPrev)
		}
		got := a.Aligned()
		first := 0
		for first < len(ref) && ref[first] == None {
			first++
		}
		for i := first + 1; i < len(got); i++ {
			if got[i] < got[i-1] {
				t.Fatalf("trial %d not non-decreasing: %v", trial, got)
			}
		}
		for i := range ref {
			if got[i] != ref[i] {
				t.Fatalf("trial %d A(%d)=%d want %d (%v)", trial, i, got[i], ref[i], got)
			}
		}
	}
}
