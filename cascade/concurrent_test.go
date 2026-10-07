package cascade

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// Concurrent deletes interleaved with keep-alive link creation. The engine
// serializes each operation; replay the observed global order on a fresh
// engine and require the identical final state, proving serializability.
func TestConcurrentDeleteAndAddSerializable(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"owned": {OutRule: RuleSetNull, InRule: RuleSetNull, KeepAlive: true},
		"casc":  {OutRule: RuleCascade, InRule: RuleCascade},
	})
	store := NewMemoryStore()
	e := NewEngine(store, cfg)
	for i := 0; i < 40; i++ {
		store.AddObject(fmt.Sprintf("n%d", i))
	}

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < 40; i += workers {
				if i%3 == 0 {
					_, _ = e.Delete(fmt.Sprintf("n%d", i))
				} else {
					src := fmt.Sprintf("n%d", (i+7)%40)
					dst := fmt.Sprintf("n%d", i)
					_ = e.AddLink(Link{ID: fmt.Sprintf("k%d", i), Type: "owned", Src: src, Dst: dst})
				}
				// A second wave deletes through cascade links, exercising
				// reads of possibly-half-built keep-alive neighborhoods.
				if i%4 == 0 {
					_ = e.AddLink(Link{ID: fmt.Sprintf("c%d", i), Type: "casc", Src: fmt.Sprintf("n%d", i), Dst: fmt.Sprintf("n%d", (i+1)%40)})
				}
			}
		}(w)
	}
	wg.Wait()

	finalSnap := store.snapshot()

	replay := NewMemoryStore()
	re := NewEngine(replay, cfg)
	for i := 0; i < 40; i++ {
		replay.AddObject(fmt.Sprintf("n%d", i))
	}
	for _, op := range e.seq {
		switch op.kind {
		case "delete":
			_, err := re.Delete(op.id)
			if (err == nil) != op.commit {
				t.Fatalf("replay divergence on delete %s", op.id)
			}
		case "add":
			err := re.AddLink(op.link)
			if (err == nil) != op.commit {
				t.Fatalf("replay divergence on add %s", op.link.ID)
			}
		}
	}

	rs := replay.snapshot()
	if !reflect.DeepEqual(finalSnap.objects, rs.objects) {
		t.Fatalf("objects not reproducible by serial replay: %v vs %v", finalSnap.objects, rs.objects)
	}
	gotLinks := map[string]Link{}
	expLinks := map[string]Link{}
	for _, l := range finalSnap.links {
		gotLinks[l.ID] = l
	}
	for _, l := range rs.links {
		expLinks[l.ID] = l
	}
	if !reflect.DeepEqual(gotLinks, expLinks) {
		t.Fatalf("links not reproducible by serial replay")
	}
}

// The dedup set that guarantees termination is a hash keyed on scheduled
// object IDs: its probe count for a fixed affected subgraph must not grow
// when the total graph is padded with large unrelated components.
func TestDedupCostIndependentOfGraphSize(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"next":  {OutRule: RuleCascade, InRule: RuleCascade},
		"owned": {OutRule: RuleSetNull, InRule: RuleSetNull, KeepAlive: true},
	})
	measure := func(padding int) int {
		store := NewMemoryStore()
		store.AddObject("r")
		store.AddObject("a")
		store.AddLink(Link{ID: "core", Type: "next", Src: "r", Dst: "a"})
		for i := 0; i < padding; i++ {
			id := fmt.Sprintf("p%d", i)
			src := fmt.Sprintf("p%d", i-1)
			store.AddObject(id)
			if i > 0 {
				store.links[fmt.Sprintf("pl%d", i)] = Link{ID: fmt.Sprintf("pl%d", i), Type: "owned", Src: src, Dst: id}
			}
		}
		p, err := plan(cfg, store.snapshot(), "r")
		if err != nil {
			t.Fatal(err)
		}
		return p.DedupProbes
	}
	base := measure(10)
	for _, n := range []int{100, 1000, 10000} {
		if got := measure(n); got != base {
			t.Fatalf("DedupProbes grew with graph size: padding 10 -> %d, padding %d -> %d", base, n, got)
		}
	}
	if base > 2 {
		t.Fatalf("DedupProbes = %d for a 2-node cascade, expected constant 1-2", base)
	}
}
