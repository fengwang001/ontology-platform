package merge

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"ontology/record"
)

func collect(t *testing.T, m *Merger) []record.Record {
	t.Helper()
	var out []record.Record
	if err := m.Merge(func(r record.Record) error {
		out = append(out, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEqualKeysAcrossRuns: the same key lands in three runs; inside each
// run its local positions restart from small values. A comparator keyed on
// run-local position would interleave wrongly; (Key, Seq) must yield the
// global arrival order.
func TestEqualKeysAcrossRuns(t *testing.T) {
	mk := func(seqs ...uint64) []record.Record {
		recs := make([]record.Record, len(seqs))
		for i, s := range seqs {
			recs[i] = record.Record{Key: "same", Value: []byte(fmt.Sprint(s)), Seq: s}
		}
		return recs
	}
	runs := [][]record.Record{
		mk(0, 3, 6),
		mk(1, 4, 7),
		mk(2, 5, 8),
	}
	var sources []Source
	for _, r := range runs {
		sources = append(sources, NewSliceSource(r))
	}
	out := collect(t, New(sources...))
	if len(out) != 9 {
		t.Fatalf("got %d records", len(out))
	}
	for i, r := range out {
		if r.Seq != uint64(i) {
			t.Fatalf("position %d: seq=%d, arrival order violated", i, r.Seq)
		}
	}
}

func TestMergeSortedAndBounded(t *testing.T) {
	cases := []struct {
		name string
		n    int
		k    int
	}{
		{"single run", 1000, 1},
		{"few runs", 5000, 5},
		{"many runs", 5000, 64},
		{"empty runs", 100, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(1))
			var sources []Source
			seq := uint64(0)
			counts := make([]int, tc.k)
			for r := range counts {
				counts[r] = tc.n / tc.k
			}
			counts[tc.k-1] += tc.n - tc.n/tc.k*tc.k
			if tc.k > 1 {
				counts[tc.k-1] += counts[0]
				counts[0] = 0 // one empty run
			}
			for r := 0; r < tc.k; r++ {
				recs := make([]record.Record, counts[r])
				for i := range recs {
					recs[i] = record.Record{Key: fmt.Sprintf("k%04d", rng.Intn(50)), Seq: seq}
					seq++
				}
				sort.Slice(recs, func(i, j int) bool { return record.Less(recs[i], recs[j]) })
				sources = append(sources, NewSliceSource(recs))
			}
			m := New(sources...)
			out := collect(t, m)
			if uint64(len(out)) != uint64(tc.n) {
				t.Fatalf("merged %d want %d", len(out), tc.n)
			}
			for i := 1; i < len(out); i++ {
				if record.Less(out[i], out[i-1]) {
					t.Fatalf("output not sorted at %d", i)
				}
			}
			bound := 4 * int64(tc.n) * int64(math.Ceil(math.Log2(float64(tc.k)+1)))
			if m.Comparisons() > bound {
				t.Fatalf("comparisons %d exceed bound %d", m.Comparisons(), bound)
			}
		})
	}
}

func TestMergeZeroRecords(t *testing.T) {
	out := collect(t, New(NewSliceSource(nil)))
	if len(out) != 0 {
		t.Fatalf("got %d records", len(out))
	}
}
