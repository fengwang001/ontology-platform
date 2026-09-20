package ontology

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// dataset builds a deterministic, size-dependent row set.
func dataset(n, salt int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{
			"k": int64((i*37 + salt) % 13),
			"s": fmt.Sprintf("row-%d-%d", salt, i),
		}
	}
	return rows
}

// TestConcurrentSortsIndependent hammers one shared Sorter from many
// goroutines, each with its own dataset and its own expected comparison
// count. Any cross-talk between per-call counters or shared state shows
// up as a mismatch (and as a race under -race).
func TestConcurrentSortsIndependent(t *testing.T) {
	sorter := New(
		SortKey{Field: "k"},
		SortKey{Field: "s", Desc: true},
	)
	const goroutines = 8
	const iterations = 25

	type baseline struct {
		rows        []map[string]any
		indices     []int
		comparisons int
	}
	baselines := make([]baseline, goroutines)
	for g := 0; g < goroutines; g++ {
		rows := dataset(20+g*3, g)
		res, err := sorter.Sort(rows)
		if err != nil {
			t.Fatalf("baseline Sort: %v", err)
		}
		baselines[g] = baseline{rows: rows, indices: res.Indices, comparisons: res.Comparisons}
	}

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			want := baselines[g]
			for it := 0; it < iterations; it++ {
				res, err := sorter.Sort(want.rows)
				if err != nil {
					errs <- fmt.Errorf("g%d: %w", g, err)
					return
				}
				if res.Comparisons != want.comparisons {
					errs <- fmt.Errorf("g%d: comparisons %d, want %d",
						g, res.Comparisons, want.comparisons)
					return
				}
				if !reflect.DeepEqual(res.Indices, want.indices) {
					errs <- fmt.Errorf("g%d: indices diverged", g)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestConcurrentDistinctCounters uses datasets whose comparison counts
// differ, so a leaked counter would be caught immediately.
func TestConcurrentDistinctCounters(t *testing.T) {
	sorter := New(SortKey{Field: "k"})
	small := dataset(4, 0)
	large := dataset(200, 1)
	smallRes, err := sorter.Sort(small)
	if err != nil {
		t.Fatalf("Sort small: %v", err)
	}
	largeRes, err := sorter.Sort(large)
	if err != nil {
		t.Fatalf("Sort large: %v", err)
	}
	if smallRes.Comparisons >= largeRes.Comparisons {
		t.Fatalf("test premise broken: small=%d large=%d",
			smallRes.Comparisons, largeRes.Comparisons)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			res, err := sorter.Sort(small)
			if err != nil || res.Comparisons != smallRes.Comparisons {
				t.Errorf("small: comparisons=%d err=%v", res.Comparisons, err)
			}
		}()
		go func() {
			defer wg.Done()
			res, err := sorter.Sort(large)
			if err != nil || res.Comparisons != largeRes.Comparisons {
				t.Errorf("large: comparisons=%d err=%v", res.Comparisons, err)
			}
		}()
	}
	wg.Wait()
}
