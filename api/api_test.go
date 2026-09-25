package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dim"
)

func E(k string, v int64) api.Entry { return api.Entry{Key: k, Val: v} }

func eightStep() *api.API {
	a, _ := api.New(100)
	for _, b := range [][]api.Entry{
		{E("a", 1), E("b", 2)}, {E("c", 3), E("d", 4)}, {E("a", 9)}, {E("e", 5), E("f", 6)},
	} {
		a.Broadcast(b)
	}
	return a
}

// TestInvariantRecompute pins invariant 1: facts read their OWN Vsn snapshot.
func TestInvariantRecompute(t *testing.T) {
	a, _ := api.New(100)
	a.Broadcast([]api.Entry{E("a", 1), E("b", 2)})
	a.Broadcast([]api.Entry{E("a", 9)})
	facts := []api.Fact{{Key: "a", Vsn: 1}, {Key: "a", Vsn: 2}, {Key: "b", Vsn: 1}, {Key: "b", Vsn: 2}}
	want := []api.Row{{Key: "a", Val: 1, Vsn: 1}, {Key: "a", Val: 9, Vsn: 2},
		{Key: "b", Val: 2, Vsn: 1}, {Key: "b", Val: 2, Vsn: 2}}
	for i, f := range facts {
		if r, err := a.Join(f); err != nil || r != want[i] {
			t.Fatalf("fact %v: row=%v err=%v want %v", f, r, err, want[i])
		}
	}
	if r, err := a.Join(api.Fact{Key: "x", Vsn: 1}); err != nil || r != (api.Row{}) || a.Missed() != 1 {
		t.Fatal("x@1 must be one miss")
	}
	if _, err := a.Join(api.Fact{Key: "a", Vsn: 3}); !errors.Is(err, dim.ErrFuture) {
		t.Fatalf("a@3 want future, got %v", err)
	}
	if !reflect.DeepEqual(a.Joined(), want) {
		t.Fatalf("stream rows %v != recompute %v", a.Joined(), want)
	}
}

// TestVersionMonotonic pins invariant 2: V only increments; rejects don't move it.
func TestVersionMonotonic(t *testing.T) {
	a, _ := api.New(100)
	var prev int64
	for i := int64(0); i < 10; i++ { // overwrite same keys => fixed-size snapshots
		v, err := a.Broadcast([]api.Entry{{Key: "a", Val: i}, {Key: "b", Val: i}})
		if err != nil || v != prev+1 {
			t.Fatalf("cast %d: v=%d err=%v prev=%d", i, v, err, prev)
		}
		prev = v
	}
	if _, err := a.Broadcast(nil); !errors.Is(err, dim.ErrEmptyBatch) {
		t.Fatal(err)
	}
	if _, err := a.Join(api.Fact{Key: "a", Vsn: prev + 1}); !errors.Is(err, dim.ErrFuture) {
		t.Fatalf("V changed after rejection: %v", err)
	}
}

// TestEvictionOrderAndCap pins invariant 3: oldest-first eviction, cap, V/V-1 floor.
func TestEvictionOrderAndCap(t *testing.T) {
	a := eightStep()
	for _, vsn := range []int64{1, 2} {
		if r, err := a.Join(api.Fact{Key: "a", Vsn: vsn}); err != nil || r != (api.Row{}) {
			t.Fatalf("v%d must be stale, got %v %v", vsn, r, err)
		}
	}
	if a.Dropped() != 2 {
		t.Fatalf("dropped=%d want 2", a.Dropped())
	}
	if r, _ := a.Join(api.Fact{Key: "a", Vsn: 3}); r.Val != 9 {
		t.Fatalf("a@3=%v want 9", r)
	}
	if r, _ := a.Join(api.Fact{Key: "e", Vsn: 4}); r.Val != 5 {
		t.Fatalf("e@4=%v want 5", r)
	}
	b, _ := api.New(20)
	b.Broadcast([]api.Entry{E("a", 1)})
	if _, err := b.Broadcast([]api.Entry{E("b", 2), E("c", 3), E("d", 4)}); !errors.Is(err, dim.ErrTooBig) {
		t.Fatalf("over-cap err=%v want ErrTooBig", err)
	}
	if r, _ := b.Join(api.Fact{Key: "a", Vsn: 1}); r.Val != 1 {
		t.Fatal("store not intact after rejection")
	}
}

// TestRejectionLeavesNoTrace pins invariant 4: distinct sentinels, zero state change.
func TestRejectionLeavesNoTrace(t *testing.T) {
	a := eightStep()
	snap := func() [4]any { return [4]any{a.View(), a.Joined(), a.Dropped(), a.Missed()} }
	calls := []func() error{
		func() error { _, e := a.Broadcast(nil); return e },
		func() error { _, e := a.Broadcast([]api.Entry{{Key: ""}}); return e },
		func() error { _, e := a.Join(api.Fact{Key: "", Vsn: 1}); return e },
		func() error { _, e := a.Join(api.Fact{Key: "a", Vsn: 99}); return e },
		func() error { _, e := api.New(0); return e },
	}
	seen := map[error]bool{}
	for _, fn := range calls {
		before := snap()
		e := fn()
		if e == nil || !reflect.DeepEqual(before, snap()) {
			t.Fatalf("nil error or state changed on rejected op: %v", e)
		}
		seen[e] = true
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinels, got %d", len(seen))
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("engine unusable after rejections: %v", err)
	}
}

// TestConcurrentJoin: concurrent Join/View give field-identical results; -race clean.
func TestConcurrentJoin(t *testing.T) {
	a, _ := api.New(1 << 30)
	a.Broadcast([]api.Entry{E("a", 11), E("b", 22)})
	const N = 32
	var wg sync.WaitGroup
	rows, views := make([]api.Row, N), make([][]api.Entry, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := api.Fact{Key: "a", Vsn: 1}
			if i%2 != 0 {
				f.Key = "b"
			}
			rows[i], _ = a.Join(f)
			views[i] = a.View()
		}(i)
	}
	wg.Wait()
	want := [2]api.Row{{Key: "a", Val: 11, Vsn: 1}, {Key: "b", Val: 22, Vsn: 1}}
	for i := range rows {
		if rows[i] != want[i%2] || !reflect.DeepEqual(views[i], a.View()) {
			t.Fatalf("goroutine %d: row=%v view mismatch", i, rows[i])
		}
	}
}
