package ontology_test

import (
	"fmt"
	"sync"
	"testing"

	ont "ontology/ontology"
)

// TestDeterminismAcrossCalls 同一状态下多次调用结果与证据逐一相同，
// 中间穿插大量与环判定无关的只读查询。
func TestDeterminismAcrossCalls(t *testing.T) {
	g := buildRandomishGraph(t, 12, 28)
	var first ont.CycleResult
	for i := 0; i < 20; i++ {
		res, err := g.HasCycle("alice")
		must(t, err)
		_, _ = g.HasCycle("bob") // 穿插只读查询
		if i == 0 {
			first = res
			continue
		}
		if res.HasCycle != first.HasCycle || fmt.Sprint(res.Cycle) != fmt.Sprint(first.Cycle) {
			t.Fatalf("result/evidence changed across calls: %+v vs %+v", first, res)
		}
	}
}

// TestMetricBoundedByVisibleSubgraph 追加大量不可见对象/链接时度量保持不变。
func TestMetricBoundedByVisibleSubgraph(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddObject("b", "T"))
	must(t, g.AddObject("c", "T"))
	must(t, g.AddLink(&ont.Link{ID: "ab", Type: linkType(g, "D"), Source: "a", Target: "b"}))
	must(t, g.AddLink(&ont.Link{ID: "bc", Type: linkType(g, "D"), Source: "b", Target: "c"}))
	grantAll(t, g, "alice", []string{"a", "b", "c"}, []string{"ab", "bc"})

	before, err := g.HasCycle("alice")
	must(t, err)

	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("x%d", i)
		must(t, g.AddObject(id, "T"))
		must(t, g.GrantExist("bob", id))
	}
	for i := 0; i < 2000; i++ {
		lid := fmt.Sprintf("xl%d", i)
		must(t, g.AddLink(&ont.Link{
			ID:     lid,
			Type:   linkType(g, "D"),
			Source: fmt.Sprintf("x%d", i),
			Target: fmt.Sprintf("x%d", (i+1)%2000),
		}))
		must(t, g.GrantTraverse("bob", lid))
	}
	after, err := g.HasCycle("alice")
	must(t, err)
	if after.VisitedObjects != before.VisitedObjects || after.VisitedLinks != before.VisitedLinks {
		t.Fatalf("metric grew with invisible subgraph: before=%+v after=%+v", before, after)
	}
	if after.VisitedObjects != 3 || after.VisitedLinks != 2 {
		t.Fatalf("metric must equal visible subgraph size, got %+v", after)
	}
}

// TestConcurrentLinearizable 并发判定与并发增删：每次返回的证据必须由
// 某个一致快照中的可遍历弧支撑（不得读到中间状态）。
func TestConcurrentLinearizable(t *testing.T) {
	g := newTestGraph(t)
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = fmt.Sprintf("o%d", i)
		must(t, g.AddObject(ids[i], "T"))
		must(t, g.GrantExist("alice", ids[i]))
	}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("e%d", i)
		must(t, g.AddLink(&ont.Link{ID: id, Type: linkType(g, "D"), Source: ids[i], Target: ids[i+1]}))
		must(t, g.GrantTraverse("alice", id))
	}
	closeID := "close"

	const readers, iterations, writerIterations = 4, 1500, 10000
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		on := false
		for i := 0; i < writerIterations; i++ {
			if on {
				_ = g.DeleteLink(closeID)
			} else {
				_ = g.AddLink(&ont.Link{ID: closeID, Type: linkType(g, "D"), Source: ids[7], Target: ids[0]})
				_ = g.GrantTraverse("alice", closeID)
			}
			on = !on
		}
	}()

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				res, view, err := g.HasCycleWithView("alice")
				if err != nil {
					t.Errorf("HasCycle error: %v", err)
					return
				}
				if res.HasCycle {
					if !view.SupportCycle(res.Cycle) {
						t.Errorf("evidence not supported by a consistent snapshot: %v", res.Cycle)
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	// 停止后状态不再变化，两次独立调用必须完全一致。
	r1, err := g.HasCycle("alice")
	must(t, err)
	r2, err := g.HasCycle("alice")
	must(t, err)
	if r1.HasCycle != r2.HasCycle || fmt.Sprint(r1.Cycle) != fmt.Sprint(r2.Cycle) {
		t.Fatalf("post-stop calls differ: %+v vs %+v", r1, r2)
	}
}
