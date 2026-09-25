package btree

import (
	"errors"
	"maps"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync"
	"testing"
)

// assertTree pins invariants 1 (structure) and 2 (ascending unique keys == set).
func assertTree(t *testing.T, tr *Tree, ref []int) {
	t.Helper()
	ks := tr.OrderedKeys()
	ok := len(ks) == len(ref) && slices.IsSorted(ks) && (tr.root == nil) == (len(ref) == 0) &&
		(tr.root == nil || tr.root.Validate() == nil && len(ks) == tr.size)
	for _, k := range ref {
		ok = ok && tr.Search(k)
	}
	if !ok {
		t.Fatalf("invariants broken: %d keys, %d reference", len(ks), len(ref))
	}
}
func mustInsert(t *testing.T, tr *Tree, ks []int) {
	for _, k := range ks {
		if _, e := tr.Insert(k); e != nil {
			t.Fatal(e)
		}
	}
}
func TestStructuralInvariants(t *testing.T) {
	for _, seq := range [][]int{{1, 2, 3, 4, 5, 6, 7}, {7, 6, 5, 4, 3, 2, 1}, {4, 1, 6, 2, 7, 3, 5}} {
		tr, _ := New(1000)
		mustInsert(t, tr, seq)
		assertTree(t, tr, seq)
	}
}
func TestRandomOperations(t *testing.T) {
	for _, seed := range []uint64{1, 42} {
		rng := rand.New(rand.NewPCG(seed, seed))
		for _, n := range []int{3, 64, 500} {
			tr, _ := New(100000)
			ref := map[int]struct{}{}
			for range n * 4 {
				k := rng.IntN(n * 2)
				if _, e := tr.Insert(k); e == nil {
					ref[k] = struct{}{}
				} else if _, e := tr.Delete(k); e == nil {
					delete(ref, k)
				}
			}
			assertTree(t, tr, slices.Sorted(maps.Keys(ref)))
			for k := range ref {
				tr.Delete(k)
			}
			assertTree(t, tr, nil)
		}
	}
}
func TestSearchMatchesBinary(t *testing.T) {
	for _, n := range []int{7, 100, 1000} {
		tr, _ := New(n * 2)
		rng := rand.New(rand.NewPCG(uint64(n), 9))
		for tr.Size() < n {
			if _, e := tr.Insert(rng.IntN(n * 3)); e != nil && !errors.Is(e, ErrDuplicateKey) {
				t.Fatal(e)
			}
		}
		ks := tr.OrderedKeys()
		for _, k := range append([]int{ks[0] - 1, ks[len(ks)-1] + 1}, ks...) {
			_, want := slices.BinarySearch(ks, k)
			if tr.Search(k) != want {
				t.Fatalf("Search(%d) disagrees with binary search (n=%d)", k, n)
			}
		}
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	if ErrDuplicateKey == ErrKeyNotFound || ErrDuplicateKey == ErrTreeFull || ErrKeyNotFound == ErrTreeFull {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	tr, _ := New(3)
	mustInsert(t, tr, []int{10, 20, 30})
	v0 := tr.visited.Load()
	untouched := func() bool {
		return tr.Size() == 3 && tr.visited.Load() == v0 &&
			slices.Equal(tr.OrderedKeys(), []int{10, 20, 30})
	}
	bad := func(fn func() error, want error) {
		t.Helper()
		if !errors.Is(fn(), want) || !untouched() {
			t.Fatalf("rejection %v changed the tree or counter", want)
		}
	}
	bad(func() error { _, e := tr.Insert(20); return e }, ErrDuplicateKey)
	bad(func() error { _, e := tr.Delete(99); return e }, ErrKeyNotFound)
	bad(func() error { _, e := tr.Insert(40); return e }, ErrTreeFull)
	if _, e := tr.Delete(20); e != nil {
		t.Fatal(e)
	}
	if _, e := tr.Insert(40); e != nil {
		t.Fatal(e)
	}
	assertTree(t, tr, []int{10, 30, 40})
}
func TestVisitedBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		tr, _ := New(m)
		for _, k := range rand.New(rand.NewPCG(uint64(m), 1)).Perm(m) {
			tr.Insert(k)
		}
		h := tr.Height()
		tr.Search(m / 2)
		if v := int(tr.visited.Load()); v < 1 || v > h || h > 40 {
			t.Fatalf("m=%d visited=%d height=%d: lookup not logarithmic", m, v, h)
		}
	}
}
func TestConcurrentInsertsAndSearches(t *testing.T) {
	const G, P = 32, 64
	tr, _ := New(G * P)
	start := make(chan struct{})
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; fn() }()
	}
	for g := range G {
		run(func() {
			for j := range P {
				tr.Insert(g*P + j)
			}
		})
		run(func() {
			for j := range P {
				for !tr.Search(g*P + j) {
					runtime.Gosched()
				}
			}
		})
	}
	close(start)
	wg.Wait()
	got := tr.OrderedKeys()
	if len(got) != G*P || !slices.IsSorted(got) || got[0] != 0 || got[len(got)-1] != G*P-1 {
		t.Fatalf("concurrent result mismatch: %v", got[:3])
	}
}
