package compensation

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// snapshot 捕获对象图的完整可观察状态用于串行性对拍。
type snapshot struct {
	props map[string]map[string]any
	ver   map[string]uint64
	clock map[string]uint64
	links map[string]bool
}

func takeSnapshot(g *Graph) snapshot {
	s := snapshot{
		props: map[string]map[string]any{},
		ver:   map[string]uint64{},
		clock: map[string]uint64{},
		links: map[string]bool{},
	}
	g.mu.RLock()
	for id, o := range g.objects {
		o.mu.RLock()
		p := make(map[string]any, len(o.props))
		for k, v := range o.props {
			p[k] = v
		}
		s.props[id] = p
		s.ver[id] = o.version
		s.clock[id] = o.clock
		o.mu.RUnlock()
	}
	for id, l := range g.links {
		s.links[id] = l.alive
	}
	g.mu.RUnlock()
	return s
}

func sameSnapshot(a, b snapshot) bool {
	if len(a.props) != len(b.props) || len(a.links) != len(b.links) {
		return false
	}
	for id, pa := range a.props {
		pb, ok := b.props[id]
		if !ok || len(pa) != len(pb) {
			return false
		}
		for k, v := range pa {
			if pb[k] != v {
				return false
			}
		}
		if a.ver[id] != b.ver[id] || a.clock[id] != b.clock[id] {
			return false
		}
	}
	for id, alive := range a.links {
		if b.links[id] != alive {
			return false
		}
	}
	return true
}

// disjointActions 构造 N 个互不相交的对象子动作；每个动作修改各自的属性并最终失败，
// 触发完整补偿。injectInverse 决定哪些步骤的逆操作会失败（会产生污染）。
func disjointActions(n int, injectInverse func(group, step int) bool) []*Action {
	actions := make([]*Action, n)
	for gIdx := 0; gIdx < n; gIdx++ {
		ops := []SubOp{
			{Kind: OpSetProperties, ObjectID: fmt.Sprintf("g%do0", gIdx),
				Sets:                 map[string]any{"a": gIdx + 1},
				InjectInverseFailure: injectInverse(gIdx, 0)},
			{Kind: OpSetProperties, ObjectID: fmt.Sprintf("g%do1", gIdx),
				Sets:                 map[string]any{"a": gIdx + 2},
				InjectInverseFailure: injectInverse(gIdx, 1)},
			{Check: func() bool { return false }}, // 必然失败，触发对前两步的补偿
		}
		actions[gIdx] = &Action{ID: fmt.Sprintf("A%d", gIdx), Ops: ops}
	}
	return actions
}

func seedDisjointGraph(g *Graph, n int) {
	for gIdx := 0; gIdx < n; gIdx++ {
		g.AddObject(fmt.Sprintf("g%do0", gIdx), map[string]any{"a": 0})
		g.AddObject(fmt.Sprintf("g%do1", gIdx), map[string]any{"a": 0})
	}
}

// runSerial 串行执行动作切片，返回最终快照与每个动作的类别序列。
func runSerial(n int,
	inject func(int, int) bool) (snapshot, []Category) {
	g := NewGraph()
	seedDisjointGraph(g, n)
	exec := New(g, nil)
	cats := make([]Category, n)
	for i, a := range disjointActions(n, inject) {
		cats[i] = exec.Execute(context.Background(), a).Category
	}
	return takeSnapshot(g), cats
}

// TestConcurrentDisjointEquivalentToSerial 互不相交子集的并发补偿结果
// 必须等价于某个串行顺序；类别集合与最终快照一致，污染标记不重不漏。
func TestConcurrentDisjointEquivalentToSerial(t *testing.T) {
	const n = 24
	inject := func(group, step int) bool {
		// 固定故障图案：组 3、17 的两个逆操作都失败；组 9 第一步失败。
		if group == 3 || group == 17 {
			return true
		}
		return group == 9 && step == 0
	}

	serialSnap, serialCats := runSerial(n, inject)

	g := NewGraph()
	seedDisjointGraph(g, n)
	exec := New(g, nil)
	actions := disjointActions(n, inject)

	var wg sync.WaitGroup
	start := make(chan struct{})
	concurCats := make([]Category, n)
	wg.Add(n)
	for i, a := range actions {
		i, a := i, a
		go func() {
			defer wg.Done()
			<-start
			concurCats[i] = exec.Execute(context.Background(), a).Category
		}()
	}
	close(start)
	wg.Wait()

	concurSnap := takeSnapshot(g)
	if !sameSnapshot(serialSnap, concurSnap) {
		t.Fatalf("concurrent final state not equivalent to any serial run\nserial=%+v\nconcur=%+v",
			serialSnap, concurSnap)
	}

	// 每个动作的判定与它单独串行执行时一致（不相交 => 逐动作等价）。
	for i := range actions {
		if concurCats[i] != serialCats[i] {
			t.Fatalf("action %d category concurrent=%s serial=%s",
				i, concurCats[i], serialCats[i])
		}
	}

	// 污染标记集合与编号不重不漏。
	for gIdx := 0; gIdx < n; gIdx++ {
		for _, step := range []int{0, 1} {
			id := fmt.Sprintf("g%do%d", gIdx, step)
			_, wantTaint := func() (struct{}, bool) {
				return struct{}{}, inject(gIdx, step)
			}()
			info, got := g.Tainted(id)
			if got != wantTaint {
				t.Fatalf("taint mismatch for %s: got=%v want=%v", id, got, wantTaint)
			}
			if got && info.EarliestStep != step {
				t.Fatalf("%s earliest step = %d, want %d", id, info.EarliestStep, step)
			}
		}
	}
}

// TestGlobalEntryNumbersNoDuplicatesConcurrent 并发下全局登记编号不重复、不缺失。
func TestGlobalEntryNumbersNoDuplicatesConcurrent(t *testing.T) {
	const n = 32
	g := NewGraph()
	seedDisjointGraph(g, n)
	exec := New(g, nil)
	actions := disjointActions(n, func(int, int) bool { return false })

	var wg sync.WaitGroup
	start := make(chan struct{})
	entrySets := make([][]uint64, n)
	wg.Add(n)
	for i, a := range actions {
		i, a := i, a
		go func() {
			defer wg.Done()
			<-start
			out := exec.Execute(context.Background(), a)
			for _, ap := range out.Applied {
				entrySets[i] = append(entrySets[i], ap.Entry)
			}
		}()
	}
	close(start)
	wg.Wait()

	seen := map[uint64]int{}
	total := 0
	for i, es := range entrySets {
		for _, e := range es {
			if prev, dup := seen[e]; dup {
				t.Fatalf("entry #%d duplicated across actions %d and %d", e, prev, i)
			}
			seen[e] = i
			total++
		}
	}
	if total != n*2 {
		t.Fatalf("entry count = %d, want %d (missing entries)", total, n*2)
	}
	// 分配的应为 1..total 的连续编号（不缺失）。
	for e := uint64(1); e <= uint64(total); e++ {
		if _, ok := seen[e]; !ok {
			t.Fatalf("entry #%d missing across concurrent flows", e)
		}
	}
}
