package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func shuffled(m int, seed int64) []int {
	ks := make([]int, m)
	for i := range ks {
		ks[i] = i + 1
	}
	rand.New(rand.NewSource(seed)).Shuffle(len(ks), func(i, j int) { ks[i], ks[j] = ks[j], ks[i] })
	return ks
}
func build(t *testing.T, max int, keys []int) *api.Tree {
	tr, err := api.New(max)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, err := tr.Insert(k); err != nil {
			t.Fatalf("insert %d: %v", k, err)
		}
	}
	return tr
}

// TestStructureInvariant pins invariant 1 via SelfCheck at several scales.
func TestStructureInvariant(t *testing.T) {
	cases := []struct{ m, del, seed int }{{10, 5, 1}, {100, 50, 2}, {1000, 700, 3}, {5000, 2500, 4}}
	for _, c := range cases {
		tr := build(t, c.m, shuffled(c.m, int64(c.seed)))
		if err := tr.SelfCheck(); err != nil {
			t.Fatalf("m=%d after inserts: %v", c.m, err)
		}
		for _, k := range shuffled(c.m, int64(c.seed+100))[:c.del] {
			if _, err := tr.Delete(k); err != nil {
				t.Fatalf("m=%d delete %d: %v", c.m, k, err)
			}
		}
		if err := tr.SelfCheck(); err != nil {
			t.Fatalf("m=%d after deletes: %v", c.m, err)
		}
	}
	if empty, _ := api.New(1); empty.SelfCheck() != nil {
		t.Fatal("empty tree should pass SelfCheck")
	}
	if _, err := api.New(0); err == nil {
		t.Fatal("New(0) should fail")
	}
}

// TestOrderedKeys pins invariant 2: ascending, duplicate-free, equal to the live set.
func TestOrderedKeys(t *testing.T) {
	cases := []struct{ m, del, seed int }{{1, 0, 7}, {50, 20, 8}, {999, 500, 9}}
	for _, c := range cases {
		tr := build(t, c.m, shuffled(c.m, int64(c.seed)))
		del := map[int]bool{}
		for _, k := range shuffled(c.m, int64(c.seed+1))[:c.del] {
			tr.Delete(k)
			del[k] = true
		}
		var want []int
		for k := 1; k <= c.m; k++ {
			if !del[k] {
				want = append(want, k)
			}
		}
		if got := tr.OrderedKeys(); !slices.Equal(got, want) {
			t.Fatalf("m=%d: got %d keys, want %d (sorted unique)", c.m, len(got), len(want))
		}
	}
}

// TestSearchMatchesReference pins invariant 3: Search == binary search of OrderedKeys.
func TestSearchMatchesReference(t *testing.T) {
	for _, m := range []int{2, 17, 300, 2000} {
		tr := build(t, m, shuffled(m, int64(m)))
		keys := tr.OrderedKeys()
		for probe := 0; probe <= m+1; probe++ {
			_, want := slices.BinarySearch(keys, probe)
			if got := tr.Search(probe); got != want {
				t.Fatalf("m=%d probe=%d: got %v want %v", m, probe, got, want)
			}
		}
	}
}

// TestFailureAtomicity pins invariant 4: distinct zero-cost sentinel rejections leave no trace.
func TestFailureAtomicity(t *testing.T) {
	tr := build(t, 5, []int{4, 2, 6, 1, 3})
	before, h := tr.OrderedKeys(), tr.Height()
	runs := []func() (int, error){
		func() (int, error) { return tr.Insert(4) },
		func() (int, error) { return tr.Delete(99) },
		func() (int, error) { return tr.Insert(7) },
	}
	wants := []error{api.ErrDuplicate, api.ErrNotFound, api.ErrFull}
	for i, run := range runs {
		if c, err := run(); c != 0 || !errors.Is(err, wants[i]) {
			t.Fatalf("cost=%d err=%v, want %v", c, err, wants[i])
		}
	}
	if api.ErrDuplicate == api.ErrNotFound || api.ErrNotFound == api.ErrFull ||
		api.ErrDuplicate == api.ErrFull {
		t.Fatal("sentinel errors not distinct")
	}
	if got := tr.OrderedKeys(); !slices.Equal(got, before) || tr.Height() != h {
		t.Fatalf("rejected ops mutated the tree: %v", got)
	}
	if _, err := tr.Delete(4); err != nil {
		t.Fatalf("tree unusable after rejections: %v", err)
	}
	if _, err := tr.Insert(7); err != nil {
		t.Fatalf("tree unusable after rejections: %v", err)
	}
}

// TestConcurrency: disjoint concurrent inserts/searches equal the serial result. No sleeps.
func TestConcurrency(t *testing.T) {
	const G, per = 32, 40
	tr, _ := api.New(G * per)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				k := g*per + i
				if _, err := tr.Insert(k); err != nil || !tr.Search(k) {
					t.Errorf("key %d: %v", k, err)
				}
			}
		}(g)
	}
	wg.Wait()
	ref, _ := api.New(G * per)
	for k := 0; k < G*per; k++ {
		ref.Insert(k)
	}
	if got := tr.OrderedKeys(); !slices.Equal(got, ref.OrderedKeys()) || !slices.IsSorted(got) {
		t.Fatalf("concurrent result differs from serial: %d keys", len(got))
	}
}
