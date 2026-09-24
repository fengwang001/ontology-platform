package api

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/merge"
	"ontology/snap"
)

func kf(ks ...int64) (r []snap.Row) { // empty-valued rows; dupes/unsort allowed
	for _, k := range ks {
		r = append(r, snap.Row{Key: k})
	}
	return
}
func randSorted(r *rand.Rand, n int) []snap.Row { // strictly increasing keys
	out := make([]snap.Row, n)
	for i, k := 0, r.Int63n(5); i < n; i++ {
		k += int64(r.Intn(3)) + 1
		out[i] = snap.Row{Key: k, Val: strconv.Itoa(r.Intn(5))}
	}
	return out
}
func eachPair(fn func(o, n []snap.Row)) { // table-driven random sorted pairs
	for _, sz := range []int{50, 500} {
		r := rand.New(rand.NewSource(int64(sz)))
		for i := 0; i < 8; i++ {
			fn(randSorted(r, sz), randSorted(r, sz/2+10))
		}
	}
}
func TestReplayEquivalence(t *testing.T) { // nails invariant 1
	eachPair(func(o, n []snap.Row) {
		if !reflect.DeepEqual(merge.Replay(o, merge.Diff(o, n)), n) {
			t.Fatal("invariant 1 mismatch")
		}
	})
}
func TestMatchesNaiveReference(t *testing.T) { // nails invariant 2
	eachPair(func(o, n []snap.Row) {
		if !reflect.DeepEqual(merge.Diff(o, n), merge.Naive(o, n)) {
			t.Fatal("invariant 2 mismatch")
		}
	})
}
func TestMinimalAndSorted(t *testing.T) { // nails invariant 3
	eachPair(func(o, n []snap.Row) {
		ch := merge.Diff(o, n)
		for i, c := range ch {
			if (c.Kind == 'U' && c.Old == c.New) || (i > 0 && ch[i-1].Key >= c.Key) {
				t.Fatal("invariant 3 mismatch")
			}
		}
	})
}

// TestSentinelErrors: four distinct errors; first front-to-back violation wins.
func TestSentinelErrors(t *testing.T) {
	if _, e := New(nil, 0); !errors.Is(e, ErrMaxChanges) {
		t.Fatalf("config: %v", e)
	}
	if _, e := New(kf(1, 1), 10); !errors.Is(e, ErrDuplicateKey) {
		t.Fatalf("initial: %v", e)
	}
	wants := []error{ErrDuplicateKey, ErrNotSorted, ErrDuplicateKey, ErrNotSorted}
	nxs := [][]int64{{4, 4}, {2, 3, 4, 5, 10, 9, 12}, {1, 1, 0}, {2, 1, 1}}
	for i, nx := range nxs { // swapped / dup-vs-unsort: first violation wins
		d, _ := New(nil, 10)
		if _, e := d.Advance(kf(nx...)); !errors.Is(e, wants[i]) {
			t.Fatalf("Advance %v: %v", nx, e)
		}
	}
	dl, _ := New(nil, 2)
	if _, e := dl.Advance(kf(1, 2, 3)); !errors.Is(e, ErrTooManyChanges) {
		t.Fatalf("limit: %v", e)
	}
}
func TestRejectedAdvanceNoStateChange(t *testing.T) { // nails invariant 4
	base := []snap.Row{{Key: 1, Val: "a"}, {Key: 3, Val: "b"}}
	d, _ := New(base, 2)
	for _, b := range [][]int64{{5, 4}, {5, 5}, {7, 8, 9}} {
		if _, e := d.Advance(kf(b...)); e == nil {
			t.Fatal("expected rejection")
		}
	}
	a, tot := d.Stats()
	if a != 0 || tot != 0 || !reflect.DeepEqual(d.Current(), base) {
		t.Fatal("rejection left a trace")
	}
	if _, e := d.Advance([]snap.Row{{Key: 1, Val: "a"}, {Key: 3, Val: "B"}}); e != nil {
		t.Fatal("differ unusable after rejections")
	}
}
func TestConcurrentReadersNoMixing(t *testing.T) { // no sleeps; version k = keys 0..k all valued k
	const K, N = 200, 8
	ver := func(k int) (v []snap.Row) {
		for j := 0; j <= k; j++ {
			v = append(v, snap.Row{Key: int64(j), Val: strconv.Itoa(k)})
		}
		return
	}
	d, _ := New(ver(0), 1e6)
	var wg sync.WaitGroup
	var bad atomic.Bool
	spawn := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	for g := 0; g < N; g++ {
		spawn(func() { // every Current must equal one whole expected version
			for last := -1; ; {
				cur := d.Current()
				idx := len(cur) - 1
				if idx < last || !reflect.DeepEqual(cur, ver(idx)) {
					bad.Store(true)
					return
				}
				if idx == K {
					return
				}
				last = idx
			}
		})
	}
	spawn(func() { // Stats + SelfCheck while the writer is still advancing
		for a, _ := d.Stats(); a < K; a, _ = d.Stats() {
			if d.SelfCheck() != nil {
				bad.Store(true)
				return
			}
		}
	})
	spawn(func() { // the single writer
		for k := 1; k <= K; k++ {
			if _, e := d.Advance(ver(k)); e != nil {
				bad.Store(true)
			}
		}
	})
	wg.Wait()
	if bad.Load() {
		t.Fatal("mixed/backwards snapshot or self-check failure")
	}
}
