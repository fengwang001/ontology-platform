package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/kgrp"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func putKeys(t *testing.T, st *api.State, n int) map[string]int64 {
	keys := map[string]int64{}
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k-%d", i)
		keys[k] = int64(i)
		must(t, st.Put(k, int64(i)))
	}
	return keys
}

// checkAssignment verifies both invariant 1 (Ranges/Owner/Get vs the naive
// per-key-group formula) and invariant 2 (contiguous cover, spread <= 1).
func checkAssignment(t *testing.T, maxP, p int) {
	t.Helper()
	st, err := api.New(maxP, p)
	must(t, err)
	keys := putKeys(t, st, 80)
	ranges := st.Ranges()
	if ranges[0][0] != 0 || ranges[p-1][1] != maxP {
		t.Fatalf("cover maxP=%d p=%d: %v", maxP, p, ranges)
	}
	q, i := maxP/p, 0
	for kg := 0; kg < maxP; kg++ {
		for kg >= ranges[i][1] {
			i++
		}
		if i != kgrp.Instance(kg, p, maxP) {
			t.Fatalf("kg=%d range=%d formula=%d", kg, i, kgrp.Instance(kg, p, maxP))
		}
	}
	for i, iv := range ranges {
		l := iv[1] - iv[0]
		if (i > 0 && iv[0] != ranges[i-1][1]) || l < q || l > q+1 {
			t.Fatalf("bad range maxP=%d p=%d: %v", maxP, p, ranges)
		}
	}
	for k, v := range keys {
		kg := int(kgrp.Hash(k) % uint32(maxP))
		gv, ok := st.Get(k)
		if st.Owner(k) != kgrp.Instance(kg, p, maxP) || !ok || gv != v {
			t.Fatalf("key %q owner/get wrong", k)
		}
	}
}

func TestRangesMatchNaive(t *testing.T) {
	for _, c := range [][2]int{{10, 3}, {10, 4}, {1, 1}, {7, 1}, {7, 7}, {13, 5}, {64, 37}} {
		checkAssignment(t, c[0], c[1])
	}
}
func TestPartitionComplete(t *testing.T) {
	for maxP := 1; maxP <= 40; maxP++ {
		for p := 1; p <= maxP; p++ {
			checkAssignment(t, maxP, p)
		}
	}
}

// TestRejectedOpsAtomic: three distinct sentinels, no state change, usable.
func TestRejectedOpsAtomic(t *testing.T) {
	for _, mp := range []int{0, -3} {
		if _, err := api.New(mp, 1); !errors.Is(err, api.ErrInvalidMaxP) {
			t.Fatalf("maxP=%d err=%v", mp, err)
		}
	}
	st, _ := api.New(10, 3)
	must(t, st.Put("x", 1))
	before := fmt.Sprint(st.Ranges())
	for _, p2 := range []int{0, -1, 11, 100} {
		if _, err := st.Rescale(p2); !errors.Is(err, api.ErrInvalidParallelism) {
			t.Fatalf("p2=%d err=%v", p2, err)
		}
	}
	if err := st.Put("", 9); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty key err=%v", err)
	}
	if api.ErrInvalidMaxP == api.ErrInvalidParallelism || api.ErrInvalidParallelism == api.ErrEmptyKey || api.ErrInvalidMaxP == api.ErrEmptyKey {
		t.Fatal("sentinel errors not distinct")
	}
	if fmt.Sprint(st.Ranges()) != before {
		t.Fatal("ranges changed after rejection")
	}
	if v, ok := st.Get("x"); !ok || v != 1 {
		t.Fatalf("value changed: %d,%v", v, ok)
	}
	must(t, st.Put("y", 2))
	_, err := st.Rescale(4)
	must(t, err)
	must(t, st.SelfCheck())
}

// snapshot builds a comparable read view exercising Ranges/Get/Owner/SelfCheck.
func snapshot(st *api.State, keys []string) string {
	s := fmt.Sprint(st.Ranges(), st.SelfCheck())
	for _, k := range keys {
		v, _ := st.Get(k)
		s += fmt.Sprintf("%s@%d=%d;", k, st.Owner(k), v)
	}
	return s
}

// TestConcurrentReadOnly: many goroutines see identical read snapshots.
func TestConcurrentReadOnly(t *testing.T) {
	st, _ := api.New(31, 3)
	keys := make([]string, 128)
	for i := range keys {
		keys[i] = fmt.Sprintf("cc-%d", i)
		must(t, st.Put(keys[i], int64(i)))
	}
	want := snapshot(st, keys)
	got := make([]string, 24)
	var wg sync.WaitGroup
	for g := range got {
		wg.Add(1)
		go func(g int) { defer wg.Done(); got[g] = snapshot(st, keys) }(g)
	}
	wg.Wait()
	for g, s := range got {
		if s != want {
			t.Fatalf("reader %d diverged", g)
		}
	}
}
