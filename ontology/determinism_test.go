package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestEvidenceMinimalAndDeterministic(t *testing.T) {
	g := newTestGraph(t)
	// 环 a->b->c->a，外加尾巴 c->d->e 与孤立点 f；证据只能是 {a,b,c}。
	addObjs(t, g, "a", "b", "c", "d", "e", "f")
	addLink(t, g, "ab", "dir", "a", "b")
	addLink(t, g, "bc", "dir", "b", "c")
	addLink(t, g, "ca", "dir", "c", "a")
	addLink(t, g, "cd", "dir", "c", "d")
	addLink(t, g, "de", "dir", "d", "e")
	grantAll(g, "c", "ab", "bc", "ca", "cd", "de")

	first, err := g.HasCycle("c")
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasCycle || !sameSet(first.Evidence, []string{"a", "b", "c"}) {
		t.Fatalf("evidence must be minimal {a,b,c}, got %+v", first.Evidence)
	}
	for i := 0; i < 8; i++ {
		r, err := g.HasCycle("c")
		if err != nil {
			t.Fatal(err)
		}
		if r.HasCycle != first.HasCycle || !sameSet(r.Evidence, first.Evidence) {
			t.Fatalf("repeat %d differs: %+v vs %+v", i, r, first)
		}
	}
	snap, _, _ := buildSnapshotForTest(t, g, "c")
	for seed := int64(0); seed < 40; seed++ {
		r, _ := snap.shuffledCopy(seed).detectCycle()
		if !r.HasCycle || !sameSet(r.Evidence, first.Evidence) {
			t.Fatalf("shuffled seed %d differs: %+v vs %+v", seed, r.Evidence, first.Evidence)
		}
	}
}

func TestHistoryOrderIrrelevant(t *testing.T) {
	// 以两种完全不同的创建历史构造同一拓扑，证据必须逐元素相同。
	build := func(order []string) Result {
		g := newTestGraph(t)
		addObjs(t, g, "a", "b", "c")
		links := map[string][3]string{
			"ab": {"dir", "a", "b"},
			"bc": {"dir", "b", "c"},
			"ca": {"dir", "c", "a"},
		}
		for _, id := range order {
			l := links[id]
			addLink(t, g, id, l[0], l[1], l[2])
		}
		grantAll(g, "c", order...)
		r, err := g.HasCycle("c")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := build([]string{"ab", "bc", "ca"})
	r2 := build([]string{"ca", "bc", "ab"})
	if !r1.HasCycle || !sameSet(r1.Evidence, r2.Evidence) {
		t.Fatalf("history must not matter: %+v vs %+v", r1, r2)
	}
}

func TestInvisibleBulkDoesNotGrowCost(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a", "b", "c")
	addLink(t, g, "ab", "dir", "a", "b")
	addLink(t, g, "bc", "dir", "b", "c")
	addLink(t, g, "ca", "dir", "c", "a")
	grant(g, "c", []string{"a", "b", "c"}, "ab", "bc", "ca")

	_, _, before := buildSnapshotForTest(t, g, "c")

	for i := 0; i < 5000; i++ {
		x := fmt.Sprintf("x%05d", i)
		y := fmt.Sprintf("y%05d", i)
		must(t, g.CreateObject(Object{ID: x, Type: "Thing"}))
		must(t, g.CreateObject(Object{ID: y, Type: "Thing"}))
		must(t, g.CreateLink(Link{ID: fmt.Sprintf("xb%05d", i), Type: "bi", Source: x, Target: y}))
	}

	res, after, err := func() (Result, Metrics, error) {
		g.mu.RLock()
		defer g.mu.RUnlock()
		_, r, m, e := g.buildSnapshot("c")
		return r, m, e
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasCycle {
		t.Fatal("visible triangle should still be detected")
	}
	if before.VisibleObjects != after.VisibleObjects || before.ScannedLinks != after.ScannedLinks {
		t.Fatalf("cost grew with invisible bulk: before %+v after %+v", before, after)
	}
	if after.VisibleObjects != 3 || after.ScannedLinks != 3 {
		t.Fatalf("metrics must only count visible accesses: %+v", after)
	}
}

func TestConcurrentCallsAndMutations(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a", "b", "c")
	grantAll(g, "c")

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			addLinkSilent(g, "ab", "a", "b")
			addLinkSilent(g, "bc", "b", "c")
			if i%2 == 0 {
				addLinkSilent(g, "ca", "c", "a")
				grantAll(g, "c", "ab", "bc", "ca")
			} else {
				grantAll(g, "c", "ab", "bc")
				_ = g.DeleteLink("ca")
			}
		}
	}()

	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				r, err := g.HasCycle("c")
				if err != nil {
					t.Errorf("HasCycle: %v", err)
					return
				}
				// 串行一致性：有环时证据必须恰好是 {a,b,c}，绝无撕裂状态。
				if r.HasCycle && !sameSet(r.Evidence, []string{"a", "b", "c"}) {
					t.Errorf("torn state evidence: %+v", r.Evidence)
					return
				}
				if r.HasCycle && len(r.Evidence) != 3 {
					t.Errorf("non-minimal evidence under concurrency: %+v", r.Evidence)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 终态：ca 被删除 => 无环。
	if err := g.DeleteLink("ca"); err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	grantAll(g, "c", "ab", "bc")
	if r, err := g.HasCycle("c"); err != nil || r.HasCycle {
		t.Fatalf("final state should be acyclic: %+v err=%v", r, err)
	}
}

func addLinkSilent(g *Graph, id, src, dst string) {
	if err := g.CreateLink(Link{ID: id, Type: "dir", Source: src, Target: dst}); err != nil &&
		!errors.Is(err, ErrAlreadyExists) {
		panic(err)
	}
}
