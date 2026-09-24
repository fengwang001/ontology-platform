package api_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// gen builds changes over k rows that never underflow (deletes only what exists).
func gen(rng *rand.Rand, n, k int) []api.Change {
	l, r := map[string]int{}, map[string]int{}
	out := make([]api.Change, 0, n)
	for i := 0; i < n; i++ {
		c := api.Change{Side: api.Side(rng.Intn(2)), Row: fmt.Sprintf("r%d", rng.Intn(k))}
		cnt := l
		if c.Side == api.R {
			cnt = r
		}
		c.Delta = 1 + rng.Intn(3)
		if cnt[c.Row] > 0 && rng.Intn(2) == 0 {
			c.Delta = -(1 + rng.Intn(cnt[c.Row]))
		}
		cnt[c.Row] += c.Delta
		out = append(out, c)
	}
	return out
}

func recompute(l, r map[string]int) map[string]int {
	want := map[string]int{}
	for x, n := range l {
		if d := n - r[x]; d > 0 {
			want[x] = d
		}
	}
	return want
}

func absi(x int) int { return max(x, -x) }

// TestPrefixConsistency: every prefix equals batch recompute (which only
// emits positive entries, proving non-negativity); outputs stay minimal.
func TestPrefixConsistency(t *testing.T) {
	type tc struct{ seed, n, k int }
	for _, p := range []tc{{1, 300, 5}, {2, 500, 50}, {3, 200, 2}} {
		t.Run(fmt.Sprintf("seed%d", p.seed), func(t *testing.T) {
			v := api.New(p.k)
			l, r := map[string]int{}, map[string]int{}
			for _, c := range gen(rand.New(rand.NewSource(int64(p.seed))), p.n, p.k) {
				outs, err := v.Apply([]api.Change{c})
				if err != nil {
					t.Fatal(err)
				}
				if c.Side == api.L {
					l[c.Row] += c.Delta
				} else {
					r[c.Row] += c.Delta
				}
				if len(outs) > 1 || (len(outs) == 1 &&
					(outs[0].Delta == 0 || absi(outs[0].Delta) > absi(c.Delta) || outs[0].Row != c.Row)) {
					t.Fatalf("non-minimal output %+v for %+v", outs, c)
				}
				if !reflect.DeepEqual(v.View(), recompute(l, r)) {
					t.Fatalf("prefix mismatch: %v vs %v", v.View(), recompute(l, r))
				}
			}
		})
	}
}

// TestRejectedBatchNoTrace: one bad entry voids the whole batch — counts,
// view and changelog unchanged — and the view keeps working afterwards.
func TestRejectedBatchNoTrace(t *testing.T) {
	v := api.New(2)
	if _, err := v.Apply([]api.Change{{Side: api.L, Row: "a", Delta: 2}}); err != nil {
		t.Fatal(err)
	}
	before := v.View()
	batches := [][]api.Change{
		{{Side: api.L, Row: "b", Delta: 1}, {Side: api.L, Row: "a", Delta: -9}}, // underflow
		{{Side: api.L, Row: "b", Delta: 1}, {Side: api.L, Row: "", Delta: 1}},   // invalid
		{{Side: api.L, Row: "b", Delta: 1}, {Side: api.L, Row: "c", Delta: 1}},  // row cap
	}
	for _, b := range batches {
		if outs, err := v.Apply(b); err == nil || outs != nil {
			t.Fatalf("batch %+v: outs=%v err=%v", b, outs, err)
		}
		if !reflect.DeepEqual(v.View(), before) {
			t.Fatalf("batch %+v left a trace: %v", b, v.View())
		}
	}
	if _, err := v.Apply([]api.Change{{Side: api.R, Row: "a", Delta: 1}}); err != nil {
		t.Fatalf("view unusable after rejections: %v", err)
	}
	if got := v.View()["a"]; got != 1 {
		t.Fatalf("view[a] = %d, want 1", got)
	}
}

// TestConcurrentViewBoundaries: readers only observe batch-boundary views.
func TestConcurrentViewBoundaries(t *testing.T) {
	const batches = 200
	v, rng := api.New(8), rand.New(rand.NewSource(7))
	l, r := map[string]int{}, map[string]int{}
	snaps := map[string]bool{fmt.Sprint(map[string]int{}): true}
	seen, mu := map[string]bool{}, sync.Mutex{}
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				k := fmt.Sprint(v.View())
				mu.Lock()
				seen[k] = true
				mu.Unlock()
			}
		}()
	}
	for b := 0; b < batches; b++ {
		batch := gen(rng, 1+rng.Intn(4), 8)
		if _, err := v.Apply(batch); err != nil {
			t.Fatal(err)
		}
		for _, c := range batch {
			if c.Side == api.L {
				l[c.Row] += c.Delta
			} else {
				r[c.Row] += c.Delta
			}
		}
		snaps[fmt.Sprint(recompute(l, r))] = true
	}
	stop.Store(true)
	wg.Wait()
	for k := range seen {
		if !snaps[k] {
			t.Fatalf("reader observed non-boundary view %q", k)
		}
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
