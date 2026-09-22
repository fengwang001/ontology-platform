package query

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/ival"
	"ontology/tree"
)

// disjointIntervals returns n non-overlapping unit intervals [2i, 2i+1).
func disjointIntervals(n int) []ival.Interval {
	ivs := make([]ival.Interval, n)
	for i := range ivs {
		ivs[i] = ival.Interval{Lo: int64(2 * i), Hi: int64(2*i + 1)}
	}
	return ivs
}

func buildDisjoint(t *testing.T, n int) *Querier {
	t.Helper()
	tr := tree.New()
	for _, x := range disjointIntervals(n) {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	return New(tr)
}

// TestStabVisitedBound: a point query hitting exactly one interval must
// visit O(height) nodes, not O(N), for N = 1000 and N = 100000.
func TestStabVisitedBound(t *testing.T) {
	const limit = 100
	prev := int64(0)
	for _, n := range []int{1000, 100000} {
		q := buildDisjoint(t, n)
		p := int64(2 * (n / 2)) // inside the middle interval
		got := q.Stab(p)
		if len(got) != 1 {
			t.Fatalf("n=%d: stab hit %d intervals want 1", n, len(got))
		}
		v := q.visited.Load()
		t.Logf("n=%d visited=%d", n, v)
		if v > limit {
			t.Fatalf("n=%d: visited %d exceeds limit %d", n, v, limit)
		}
		if prev > 0 && v > 2*prev {
			t.Fatalf("visited grew too fast: %d -> %d for 100x data", prev, v)
		}
		prev = v
	}
}

// TestOverlapVisitedScalesWithMatches: a query matching M of N intervals
// must visit O(M + height) nodes, far below N.
func TestOverlapVisitedScalesWithMatches(t *testing.T) {
	const n = 100000
	q := buildDisjoint(t, n)
	for _, m := range []int{1, 50} {
		lo := int64(2 * (n / 2))
		hi := lo + int64(2*m) // covers exactly m disjoint intervals
		got, err := q.Overlap(ival.Interval{Lo: lo, Hi: hi})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != m {
			t.Fatalf("m=%d: got %d matches", m, len(got))
		}
		v := q.visited.Load()
		t.Logf("m=%d visited=%d", m, v)
		if limit := int64(4*m + 200); v > limit {
			t.Fatalf("m=%d: visited %d exceeds O(M+height) limit %d", m, v, limit)
		}
		if v > n/100 {
			t.Fatalf("m=%d: visited %d is not far below N=%d", m, v, n)
		}
	}
}

// TestConcurrentQueries runs the same query set from many goroutines;
// all results must be bitwise identical. No sleeps are used.
func TestConcurrentQueries(t *testing.T) {
	tr := tree.New()
	rng := slices.Clone(disjointIntervals(2000))
	for i := range rng {
		rng[i].Payload = "x"
	}
	for _, x := range rng {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	q := New(tr)
	ref := q.Stab(2001)
	refOv, err := q.Overlap(ival.Interval{Lo: 100, Hi: 300})
	if err != nil {
		t.Fatal(err)
	}
	const g = 32
	var wg sync.WaitGroup
	errs := make(chan string, g)
	for k := 0; k < g; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				if got := q.Stab(2001); !slices.Equal(got, ref) {
					errs <- "stab result diverged"
					return
				}
				got, err := q.Overlap(ival.Interval{Lo: 100, Hi: 300})
				if err != nil || !slices.Equal(got, refOv) {
					errs <- "overlap result diverged"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestBatchLimit(t *testing.T) {
	tr, _ := build(t, []ival.Interval{iv(0, 10)})
	q := New(tr, WithMaxBatch(2))
	qs := []ival.Interval{iv(1, 2), iv(3, 4), iv(5, 6)}
	if _, err := q.Batch(qs); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("got %v want ErrBatchTooLarge", err)
	}
	out, err := q.Batch(qs[:2]) // structure stays usable after rejection
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || len(out[0]) != 1 || len(out[1]) != 1 {
		t.Fatalf("unexpected batch result %v", out)
	}
	if _, err := q.Batch([]ival.Interval{iv(9, 1)}); !errors.Is(err, ival.ErrInvalid) {
		t.Fatalf("invalid interval in batch: got %v", err)
	}
}
