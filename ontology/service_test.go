package ontology

import (
	"errors"
	"testing"
)

// buildHiddenRingGraph 构建：
//
//	s --l1(可见)--> a --l2(可见)--> b
//	                     b --h1(不可见)--> c --h2(不可见)--> a
//
// 调用方只持有 L1：可见子图是 s->a->b 的链（b 看似死路），
// 但完整图上 b->c->a 与 a->b 构成真实环路。
func buildHiddenRingGraph(t *testing.T) *GraphStore {
	t.Helper()
	g := NewGraphStore()
	for _, o := range []string{"s", "a", "b", "c"} {
		g.AddObject(o)
	}
	mustAdd(t, g, link("l1", "s", "a", "L1"))
	mustAdd(t, g, link("l2", "a", "b", "L1"))
	mustAdd(t, g, link("h1", "b", "c", "SECRET"))
	mustAdd(t, g, link("h2", "c", "a", "SECRET"))
	return g
}

func mustAdd(t *testing.T, g *GraphStore, lk Link) {
	t.Helper()
	if err := g.AddLink(lk); err != nil {
		t.Fatalf("add link %s: %v", lk.ID, err)
	}
}

// 真实环路仅由不可见链接闭合：必须终止并判定为环，且不泄露任何
// 不可见链接或对象（c、h1、h2、SECRET 都不得出现在结果中）。
func TestHiddenOnlyCycleDetectedAndConcealed(t *testing.T) {
	g := buildHiddenRingGraph(t)
	logger := &MemoryLogger{}
	svc := NewService(g, logger)

	resp, err := svc.Traverse(TraverseRequest{
		CallerID: "u-low", Start: "s", Labels: labelSet("L1"), MaxDepth: 8,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	var cycle *PathResult
	for i := range resp.Paths {
		p := resp.Paths[i]
		if p.Verdict == VerdictHiddenCycle {
			cycle = &resp.Paths[i]
		}
		for _, h := range p.Prefix {
			if h.Label == "SECRET" || h.To == "c" || h.LinkID == "h1" || h.LinkID == "h2" {
				t.Fatalf("hidden info leaked: %+v", h)
			}
		}
	}
	if cycle == nil {
		t.Fatalf("expected a hidden-cycle termination, got %+v", resp.Paths)
	}
	if cycle.End != "b" {
		t.Fatalf("cycle frontier should be visible node b, got %q", cycle.End)
	}
	if len(cycle.Prefix) != 2 {
		t.Fatalf("expected visible prefix s->a->b, got %+v", cycle.Prefix)
	}
	if cycle.Hidden != HiddenCycle {
		t.Fatalf("expected HiddenCycle marker, got %v", cycle.Hidden)
	}

	// 日志必须记录调用方权限、输入与判定依据，且同样不含隐藏信息。
	entries := logger.Snapshot()
	if len(entries) != 1 || entries[0].Start != "s" || len(entries[0].Labels) != 1 {
		t.Fatalf("unexpected log entries: %+v", entries)
	}
}

// 部分不可见路径（PARTIAL）与真实环路终止（CYCLE）在同一次遍历中共存。
func TestPartialAndHiddenCycleCoexist(t *testing.T) {
	g := buildHiddenRingGraph(t)
	// 在 a 上增加一条不可见但不闭合的分支：a->d（d 死路）。
	g.AddObject("d")
	mustAdd(t, g, link("h3", "a", "d", "SECRET"))

	svc := NewService(g, &MemoryLogger{})
	resp, err := svc.Traverse(TraverseRequest{
		Start: "s", Labels: labelSet("L1"), MaxDepth: 8,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	seenPartial, seenCycle := false, false
	for _, p := range resp.Paths {
		switch {
		case p.Verdict == VerdictHiddenCycle:
			seenCycle = true
		case p.Verdict == VerdictPartialInvisible:
			if p.End != "a" {
				t.Fatalf("partial frontier should be a, got %q", p.End)
			}
			seenPartial = true
		}
	}
	if !seenPartial || !seenCycle {
		t.Fatalf("expected both PARTIAL(at a) and CYCLE(at b), got %+v", resp.Paths)
	}
}

// 交叉验证：不同权限集合的调用方可见结果可以不同，但对同一条
// 完整图真实环路，凡能观察到该路径前缀的调用方都必须一致判定成环。
func TestCrossCallerConsistency(t *testing.T) {
	g := buildHiddenRingGraph(t)
	svc := NewService(g, &MemoryLogger{})
	snap := g.Snapshot()

	callers := map[string]map[string]struct{}{
		"low":    labelSet("L1"),
		"medium": labelSet("L1", "SECRET"),
		"extra":  labelSet("L1", "OTHER"),
	}
	results := map[string][]PathResult{}
	for name, labels := range callers {
		resp, err := svc.traverseSnapshot(snap, TraverseRequest{
			CallerID: name, Start: "s", Labels: labels, MaxDepth: 8,
		})
		if err != nil {
			t.Fatalf("caller %s: %v", name, err)
		}
		results[name] = resp.Paths
	}

	// 中权限能看到完整环（可见环判定）。
	if !hasVerdict(results["medium"], VerdictVisibleCycle) {
		t.Fatalf("medium caller should see visible cycle: %+v", results["medium"])
	}
	// 低权限与额外标签权限看不到隐藏边，但都必须在前沿 b 得到“成环终止”。
	for _, name := range []string{"low", "extra"} {
		if !hasHiddenCycleAt(results[name], "b") {
			t.Fatalf("caller %s must agree cycle terminates at b: %+v", name, results[name])
		}
	}
	// 可见性差异：低/额外权限看不到 c，中权限可以。
	if pathsContainObject(results["low"], "c") || pathsContainObject(results["extra"], "c") {
		t.Fatalf("c must be invisible to low/extra")
	}
	if !pathsContainObject(results["medium"], "c") {
		t.Fatalf("c should be visible to medium")
	}
}

func hasVerdict(paths []PathResult, v Verdict) bool {
	for _, p := range paths {
		if p.Verdict == v {
			return true
		}
	}
	return false
}

func hasHiddenCycleAt(paths []PathResult, node string) bool {
	for _, p := range paths {
		if p.Verdict == VerdictHiddenCycle && p.End == node {
			return true
		}
	}
	return false
}

func pathsContainObject(paths []PathResult, obj string) bool {
	for _, p := range paths {
		if p.End == obj {
			return true
		}
		for _, h := range p.Prefix {
			if h.To == obj || h.From == obj {
				return true
			}
		}
	}
	return false
}

// 四类错误按固定次序只报第一类。
func TestErrorPrecedence(t *testing.T) {
	g := NewGraphStore()
	g.AddObject("s")
	svc := NewService(g, &MemoryLogger{})

	cases := []struct {
		name string
		req  TraverseRequest
		want error
	}{
		{"missing start", TraverseRequest{Start: "nope", Labels: labelSet("L"), MaxDepth: 1}, ErrStartNotFound},
		{"empty labels", TraverseRequest{Start: "s", Labels: map[string]struct{}{}, MaxDepth: 1}, ErrEmptyLabels},
		{"bad depth", TraverseRequest{Start: "s", Labels: labelSet("L"), MaxDepth: 0}, ErrInvalidDepth},
		{"start invisible", TraverseRequest{Start: "s", Labels: labelSet("OTHER"), MaxDepth: 1}, ErrStartNotVisible},
	}
	// 起始对象无任何可见链接 => ErrStartNotVisible（次序最后的情形）。
	for _, tc := range cases {
		_, err := svc.Traverse(tc.req)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}

	// 组合冲突时必须报次序最前者：对象缺失 + 空标签 + 坏深度 => StartNotFound。
	_, err := svc.Traverse(TraverseRequest{Start: "ghost", Labels: nil, MaxDepth: -3})
	if !errors.Is(err, ErrStartNotFound) {
		t.Fatalf("precedence: want StartNotFound, got %v", err)
	}
	// 对象存在 + 空标签 + 坏深度 => EmptyLabels。
	_, err = svc.Traverse(TraverseRequest{Start: "s", Labels: nil, MaxDepth: -3})
	if !errors.Is(err, ErrEmptyLabels) {
		t.Fatalf("precedence: want EmptyLabels, got %v", err)
	}
}

// 完全可见且不成环的路径必须正常返回，不因内部核对产生额外限制。
func TestFullyVisibleNonCyclicPath(t *testing.T) {
	g := NewGraphStore()
	for _, o := range []string{"s", "a", "b"} {
		g.AddObject(o)
	}
	mustAdd(t, g, link("l1", "s", "a", "L1"))
	mustAdd(t, g, link("l2", "a", "b", "L1"))
	svc := NewService(g, &MemoryLogger{})
	resp, err := svc.Traverse(TraverseRequest{Start: "s", Labels: labelSet("L1"), MaxDepth: 5})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(resp.Paths) != 1 || resp.Paths[0].Verdict != VerdictNonCyclic {
		t.Fatalf("expected single non-cyclic path, got %+v", resp.Paths)
	}
	if resp.Paths[0].End != "b" || len(resp.Paths[0].Prefix) != 2 {
		t.Fatalf("unexpected path: %+v", resp.Paths[0])
	}
}

// 日志必须记录调用方权限、输入与各条路径判定依据。
func TestLoggerRecordsBasis(t *testing.T) {
	g := buildHiddenRingGraph(t)
	logger := &MemoryLogger{}
	svc := NewService(g, logger)
	_, err := svc.Traverse(TraverseRequest{
		CallerID: "u-log", Start: "s", Labels: labelSet("L1"), MaxDepth: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := logger.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("want 1 log entry, got %d", len(entries))
	}
	e := entries[0]
	if e.CallerID != "u-log" || e.Start != "s" || e.MaxDepth != 8 {
		t.Fatalf("日志缺少输入字段: %+v", e)
	}
	if len(e.Labels) != 1 || e.Labels[0] != "L1" {
		t.Fatalf("日志缺少调用方权限: %+v", e.Labels)
	}
	if len(e.Paths) == 0 {
		t.Fatalf("日志缺少各路径判定依据")
	}
	foundBasis := false
	for _, p := range e.Paths {
		if p.Verdict == VerdictHiddenCycle {
			foundBasis = true
		}
	}
	if !foundBasis {
		t.Fatalf("日志未记录环路判定依据")
	}
}
