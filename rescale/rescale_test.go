package rescale

import (
	"fmt"
	"testing"

	"ontology/kgrp"
)

func mustPut(t *testing.T, st *State, k string, v int64) {
	t.Helper()
	if err := st.Put(k, v); err != nil {
		t.Fatal(err)
	}
}

// keyForKG scans generated keys until one hashes into kg.
func keyForKG(kg, maxP int, used map[string]bool) string {
	for j := 0; ; j++ {
		k := fmt.Sprintf("kg%d-%d", kg, j)
		if !used[k] && int(kgrp.Hash(k)%uint32(maxP)) == kg {
			used[k] = true
			return k
		}
	}
}

// TestEightKeyScenario pins the NOTES.md derivation for maxP=10, 3 -> 4.
func TestEightKeyScenario(t *testing.T) {
	wantKG := []int{6, 7, 8, 9, 0, 1, 2, 3}
	st, _ := New(10, 3)
	for n := 1; n <= 8; n++ {
		k := fmt.Sprintf("u%d", n)
		if kg := int(kgrp.Hash(k) % 10); kg != wantKG[n-1] {
			t.Fatalf("%s kg=%d want %d", k, kg, wantKG[n-1])
		}
		mustPut(t, st, k, int64(n))
	}
	plan, moved, err := st.Rescale(4)
	if err != nil || moved != 4 {
		t.Fatalf("moved=%d err=%v", moved, err)
	}
	got := []int{}
	for i, ip := range plan {
		if ip.Instance != i {
			t.Fatalf("plan not ordered: %+v", plan)
		}
		prev := -1
		for _, mv := range ip.Incoming {
			if mv.KeyGroup <= prev || mv.From != kgrp.Instance(mv.KeyGroup, 3, 10) {
				t.Fatalf("bad move %+v", mv)
			}
			prev, got = mv.KeyGroup, append(got, mv.KeyGroup)
		}
	}
	if fmt.Sprint(got) != "[3 5 6 8 9]" {
		t.Fatalf("groups=%v want [3 5 6 8 9]", got)
	}
	for n := int64(1); n <= 8; n++ {
		if v, ok := st.Get(fmt.Sprintf("u%d", n)); !ok || v != n {
			t.Fatalf("u%d=%d,%v not conserved", n, v, ok)
		}
	}
}

// TestRescaleConservation is table-driven: the plan lists exactly the
// formula-derived migrating groups, moved count matches, keys/values survive.
func TestRescaleConservation(t *testing.T) {
	for _, c := range [][3]int{{10, 3, 4}, {10, 4, 3}, {128, 4, 5}, {13, 7, 2}, {16, 8, 8}} {
		maxP, p1, p2 := c[0], c[1], c[2]
		st, err := New(maxP, p1)
		if err != nil {
			t.Fatal(err)
		}
		keys := map[string]int64{}
		for n := 0; n < 120; n++ {
			k := fmt.Sprintf("k-%d-%d", maxP, n)
			keys[k] = int64(n)
			mustPut(t, st, k, keys[k])
		}
		formula := 0
		for k := range keys {
			kg := int(kgrp.Hash(k) % uint32(maxP))
			if kgrp.Instance(kg, p1, maxP) != kgrp.Instance(kg, p2, maxP) {
				formula++
			}
		}
		plan, moved, err := st.Rescale(p2)
		if err != nil || moved != formula || len(plan) != p2 {
			t.Fatalf("moved=%d want=%d len=%d err=%v", moved, formula, len(plan), err)
		}
		for i, ip := range plan {
			s, e := kgrp.RangeBounds(i, p2, maxP)
			if ip.Start != s || ip.End != e {
				t.Fatalf("plan[%d]=%+v", i, ip)
			}
		}
		for k, v := range keys {
			kg := int(kgrp.Hash(k) % uint32(maxP))
			gv, ok := st.Get(k)
			if !ok || gv != v || st.Owner(k) != kgrp.Instance(kg, p2, maxP) {
				t.Fatalf("key %q wrong after rescale", k)
			}
		}
	}
}

// TestAccessCounter: one key per migrating group and m keys in staying
// groups; the unexported counter must stay constant as m grows. It is only
// readable here, never through any exported API.
func TestAccessCounter(t *testing.T) {
	const maxP, p1, p2 = 128, 4, 5
	mig, used := 0, map[string]bool{}
	for kg := 0; kg < maxP; kg++ {
		if kgrp.Instance(kg, p1, maxP) != kgrp.Instance(kg, p2, maxP) {
			mig++
		}
	}
	first := -1
	for _, m := range []int{100, 1000, 10000} {
		st, _ := New(maxP, p1)
		for kg := 0; kg < maxP; kg++ {
			if kgrp.Instance(kg, p1, maxP) != kgrp.Instance(kg, p2, maxP) {
				mustPut(t, st, keyForKG(kg, maxP, used), 1)
			}
		}
		for j, put := 0, 0; put < m; j++ {
			k := fmt.Sprintf("bulk-%d-%d", maxP, j)
			kg := int(kgrp.Hash(k) % maxP)
			if kgrp.Instance(kg, p1, maxP) == kgrp.Instance(kg, p2, maxP) && !used[k] {
				mustPut(t, st, k, int64(j))
				put++
			}
		}
		if _, _, err := st.Rescale(p2); err != nil {
			t.Fatal(err)
		} else if st.lastVisited != mig {
			t.Fatalf("m=%d visited=%d want %d (migrated keys + const)", m, st.lastVisited, mig)
		}
		if first < 0 {
			first = st.lastVisited
		} else if st.lastVisited != first {
			t.Fatalf("visited grew with m: %d vs %d", st.lastVisited, first)
		}
	}
}
