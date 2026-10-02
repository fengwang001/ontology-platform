package consistenthash_test

import (
	"errors"
	"testing"

	"ontology/consistenthash"
)

func mustAdd(t *testing.T, r *consistenthash.Ring, id int64, points []uint64) {
	t.Helper()
	if err := r.AddNode(id, points); err != nil {
		t.Fatalf("AddNode(%d, %v): %v", id, points, err)
	}
}

// TestWorkedExample replays the full scenario from the specification.
func TestWorkedExample(t *testing.T) {
	r, err := consistenthash.New(5, 4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustAdd(t, r, 1, []uint64{10, 50})
	mustAdd(t, r, 2, []uint64{30, 70})
	mustAdd(t, r, 3, []uint64{90})

	puts := []struct {
		key string
		pos uint64
		own int64
	}{
		{"k1", 5, 1},
		{"k2", 8, 2},
		{"k3", 12, 2},
		{"k4", 14, 1},
		{"k5", 95, 1},
		{"k6", 92, 2},
		{"k7", 91, 3},
	}
	for _, p := range puts {
		if err := r.Put(p.key, p.pos); err != nil {
			t.Fatalf("Put %s: %v", p.key, err)
		}
		got, _ := r.Lookup(p.key)
		if got != p.own {
			t.Fatalf("after Put %s: owner=%d want %d; loads=%v", p.key, got, p.own, r.Load())
		}
	}
	if loads := r.Load(); loads[1] != 3 || loads[2] != 3 || loads[3] != 1 {
		t.Fatalf("loads before remove: %v", loads)
	}

	if err := r.RemoveNode(2); err != nil {
		t.Fatalf("RemoveNode(2): %v", err)
	}
	want := map[string]int64{"k1": 1, "k2": 1, "k3": 3, "k4": 1, "k5": 1, "k6": 1, "k7": 3}
	for k, w := range want {
		got, _ := r.Lookup(k)
		if got != w {
			t.Fatalf("after remove node2, %s owner=%d want %d; loads=%v", k, got, w, r.Load())
		}
	}
	if loads := r.Load(); loads[1] != 5 || loads[3] != 2 {
		t.Fatalf("loads after remove: %v", loads)
	}

	before := r.Load()
	if err := r.AddNode(4, []uint64{60}); err != nil {
		t.Fatalf("AddNode(4): %v", err)
	}
	for k, w := range want {
		got, _ := r.Lookup(k)
		if got != w {
			t.Fatalf("AddNode moved %s: %d -> %d", k, w, got)
		}
	}
	if loads := r.Load(); loads[1] != before[1] || loads[3] != before[3] || loads[4] != 0 {
		t.Fatalf("AddNode changed loads: before=%v after=%v", before, loads)
	}

	migs, excess, err := r.Rebalance(10)
	if err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	if excess != 0 {
		t.Fatalf("excess=%d want 0", excess)
	}
	wantMigs := []consistenthash.Migration{
		{Key: "k6", From: 1, To: 4},
		{Key: "k5", From: 1, To: 4},
	}
	if len(migs) != len(wantMigs) {
		t.Fatalf("migrations=%v want %v", migs, wantMigs)
	}
	for i := range wantMigs {
		if migs[i] != wantMigs[i] {
			t.Fatalf("migration[%d]=%+v want %+v; all=%v", i, migs[i], wantMigs[i], migs)
		}
	}
	if loads := r.Load(); loads[1] != 3 || loads[3] != 2 || loads[4] != 2 {
		t.Fatalf("loads after rebalance: %v", loads)
	}

	if seq6, _ := r.Seq("k6"); seq6 != 6 {
		t.Fatalf("seq of k6 = %d want 6", seq6)
	}
}

// TestRebalanceLimitTruncation verifies limit=1 performs exactly one
// migration and reports the remaining excess.
func TestRebalanceLimitTruncation(t *testing.T) {
	r, _ := consistenthash.New(5, 4)
	mustAdd(t, r, 1, []uint64{10, 50})
	mustAdd(t, r, 2, []uint64{30, 70})
	mustAdd(t, r, 3, []uint64{90})
	for _, p := range []struct {
		k string
		p uint64
	}{{"k1", 5}, {"k2", 8}, {"k3", 12}, {"k4", 14}, {"k5", 95}, {"k6", 92}, {"k7", 91}} {
		if err := r.Put(p.k, p.p); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RemoveNode(2); err != nil {
		t.Fatal(err)
	}
	if err := r.AddNode(4, []uint64{60}); err != nil {
		t.Fatal(err)
	}

	migs, excess, err := r.Rebalance(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 1 || migs[0] != (consistenthash.Migration{Key: "k6", From: 1, To: 4}) {
		t.Fatalf("migrations=%v", migs)
	}
	if excess != 1 {
		t.Fatalf("excess=%d want 1; loads=%v", excess, r.Load())
	}
	if loads := r.Load(); loads[1] != 4 {
		t.Fatalf("node1 load=%d want 4: %v", loads[1], loads)
	}
}

// TestWrapAroundAndFullSkips hashes every key past the largest point and
// checks wrap-around and skipping full points around the whole ring.
func TestWrapAroundAndFullSkips(t *testing.T) {
	r, _ := consistenthash.New(1, 1)
	mustAdd(t, r, 1, []uint64{10})
	mustAdd(t, r, 2, []uint64{20})
	mustAdd(t, r, 3, []uint64{30})

	for i := 0; i < 6; i++ {
		if err := r.Put(string(rune('a'+i)), 100); err != nil {
			t.Fatal(err)
		}
	}
	if loads := r.Load(); loads[1] != 2 || loads[2] != 2 || loads[3] != 2 {
		t.Fatalf("loads=%v want 2/2/2", loads)
	}

	r2, _ := consistenthash.New(2, 1)
	mustAdd(t, r2, 1, []uint64{10, 40})
	mustAdd(t, r2, 2, []uint64{20})
	for i := 0; i < 4; i++ {
		if err := r2.Put(string(rune('a'+i)), 25); err != nil {
			t.Fatal(err)
		}
	}
	if loads := r2.Load(); loads[1] < 1 || loads[1]+loads[2] != 4 {
		t.Fatalf("loads=%v", loads)
	}
}

// TestLoadEqualsCapIsFull: a node whose load equals cap is skipped.
func TestLoadEqualsCapIsFull(t *testing.T) {
	r, _ := consistenthash.New(1, 1)
	mustAdd(t, r, 1, []uint64{10})
	mustAdd(t, r, 2, []uint64{20})

	if err := r.Put("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Put("b", 0); err != nil {
		t.Fatal(err)
	}
	if own, _ := r.Lookup("b"); own != 2 {
		t.Fatalf("b owner=%d want 2 (load==cap must count as full)", own)
	}
}

// TestDeleteMovesNothing: Delete decrements load and never migrates.
func TestDeleteMovesNothing(t *testing.T) {
	r, _ := consistenthash.New(1, 1)
	mustAdd(t, r, 1, []uint64{10})
	mustAdd(t, r, 2, []uint64{20})
	_ = r.Put("a", 0)
	_ = r.Put("b", 0)
	bOwner, _ := r.Lookup("b")
	if err := r.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if id, _ := r.Lookup("b"); id != bOwner {
		t.Fatalf("b moved after Delete: %d -> %d", bOwner, id)
	}
	if _, err := r.Lookup("a"); !errors.Is(err, consistenthash.ErrKeyNotFound) {
		t.Fatalf("Lookup deleted key: %v", err)
	}
	if loads := r.Load(); loads[1]+loads[2] != 1 {
		t.Fatalf("total load=%d want 1: %v", loads[1]+loads[2], loads)
	}
	if err := r.Delete("a"); !errors.Is(err, consistenthash.ErrKeyNotFound) {
		t.Fatalf("second delete err=%v", err)
	}
	if err := r.Delete(""); !errors.Is(err, consistenthash.ErrInvalidKey) {
		t.Fatalf("delete empty err=%v", err)
	}
}
