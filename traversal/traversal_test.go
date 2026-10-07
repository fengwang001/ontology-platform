package traversal

import (
	"context"
	"errors"
	"testing"
	"time"
)

const tRel LinkTypeID = "rel"

func dirs(d Direction) map[LinkTypeID]Direction {
	return map[LinkTypeID]Direction{tRel: d}
}

// TestTraversalRejectsCancelledContext 已取消的上下文在遍历开始前被拒绝，
// 不产生任何路径。
func TestTraversalRejectsCancelledContext(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "self", tRel, "A", "A")

	logger := NewMemoryLogger()
	svc := NewService(g, logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := svc.Traverse(ctx, TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 3,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if res != nil {
		t.Fatalf("cancelled request must yield no result")
	}
	entry := logger.Entries()[0]
	if entry.Accepted || len(entry.Paths) != 0 {
		t.Fatalf("cancelled request must not produce partial paths: %#v", entry)
	}

	// 超时上下文同样在开始前被拒绝。
	ctx2, cancel2 := context.WithTimeout(context.Background(), -time.Second)
	defer cancel2()
	if _, err := svc.Traverse(ctx2, TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 3,
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
}

// TestSelfLoopDetectedOnFirstHop 自环必须在第一跳即被判为真实环路：
// 路径只有 A->A 一跳，且不允许因为先扩展再判定而漏掉。
func TestSelfLoopDetectedOnFirstHop(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "self", tRel, "A", "A")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Paths) != 1 {
		t.Fatalf("want exactly one path, got %d", len(res.Paths))
	}
	p := res.Paths[0]
	if p.Status != StatusCycle {
		t.Fatalf("want cycle status, got %q", p.Status)
	}
	if p.Depth != 1 {
		t.Fatalf("self loop must be detected on the first hop, got depth %d", p.Depth)
	}
	if p.Cycle == nil || p.Cycle.RepeatedObject != "A" || p.Cycle.AncestorIndex != 0 {
		t.Fatalf("bad cycle info: %#v", p.Cycle)
	}
	if len(p.Cycle.AncestorSequence) != 1 || p.Cycle.AncestorSequence[0] != "A" {
		t.Fatalf("ancestor sequence must be [A], got %v", p.Cycle.AncestorSequence)
	}
}

// TestParallelLinksMixed 同一对 A-B 之间两条平行链接：
// 一条经 B 上的自环构成环路（返回 A? 不——自环闭合于 B），
// 另一条继续到叶子 C 自然终止；两条平行链接互不影响。
func TestParallelLinksMixed(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C")
	mustAddLinkTypes(t, g, tRel)
	// 两条类型相同、方向相同的平行链接 A->B。
	mustAddLink(t, g, "p1", tRel, "A", "B")
	mustAddLink(t, g, "p2", tRel, "A", "B")
	// B 上的自环：路径 A->p1->B->bs->B 形成真实环路。
	mustAddLink(t, g, "bs", tRel, "B", "B")
	// B->C 叶子：另一条平行链接 A->p2->B->C 自然终止。
	mustAddLink(t, g, "bc", tRel, "B", "C")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	counts := statusCounts(res)
	if counts[StatusCycle] != 2 {
		// 每条平行链接各自独立地扩展到 B，B 上的自环因此会沿两条
		// 平行路径分别闭合一次：共两条环路路径。
		t.Fatalf("want 2 cycle paths, got counts=%v paths=%#v", counts, shapes(res))
	}
	if counts[StatusBoundary] != 2 {
		// 每条平行路径又各自经由 B->C 终止于叶子 C：共两条边界路径。
		t.Fatalf("want 2 boundary paths, got counts=%v paths=%#v", counts, shapes(res))
	}
	if counts[StatusDepthLimit] != 0 {
		t.Fatalf("want no depth-limited paths, got %v", counts)
	}
}

// TestDiamondConvergenceIsNotCycle 菱形 A->B->D, A->C->D：
// D 经两条不相交路径被到达两次，但 D 不在任何一条路径的祖先序列中，
// 故两条路径都必须自然终止，绝不能被误判为环路。
func TestDiamondConvergenceIsNotCycle(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C", "D")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")
	mustAddLink(t, g, "ac", tRel, "A", "C")
	mustAddLink(t, g, "bd", tRel, "B", "D")
	mustAddLink(t, g, "cd", tRel, "C", "D")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	counts := statusCounts(res)
	if counts[StatusBoundary] != 2 {
		t.Fatalf("want 2 boundary paths, got counts=%v paths=%#v", counts, shapes(res))
	}
	if counts[StatusCycle] != 0 || counts[StatusDepthLimit] != 0 {
		t.Fatalf("diamond must contain neither cycles nor truncation: %v", counts)
	}
}

// TestRealCycleVsMultipath 显式区分两种现象：
// 菱形汇聚（A->B->D, A->C->D）叠加一条真实闭合边 D->B。
// 经 B 分支 D->B 闭合为环；经 C 分支 D->B 指向的 B 不在
// 祖先序列 [A,C,D] 中，因此不是环，继续从 B 扩展。
func TestRealCycleVsMultipath(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C", "D")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")
	mustAddLink(t, g, "ac", tRel, "A", "C")
	mustAddLink(t, g, "bd", tRel, "B", "D")
	mustAddLink(t, g, "cd", tRel, "C", "D")
	mustAddLink(t, g, "db", tRel, "D", "B")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 6,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	counts := statusCounts(res)
	// A-B-D-B：真实环路，闭合于 B（B 在该路径祖先 [A,B,D] 中）。
	// A-C-D-B：此时 B 不在祖先 [A,C,D] 中，必须继续扩展；随后 B->D
	// 才闭合于 D（D 进入祖先 [A,C,D,B]）。若实现误用全局 visited，
	// 第二条路径会在 D 或 B 被提前误杀。
	if counts[StatusCycle] != 2 {
		t.Fatalf("want 2 cycle paths, got %v (%#v)", counts, shapes(res))
	}
	wantShapes := []pathShape{
		{nodes: []ObjectID{"A", "B", "D", "B"}, links: []LinkID{"ab", "bd", "db"}, status: StatusCycle, repeat: "B"},
		{nodes: []ObjectID{"A", "C", "D", "B", "D"}, links: []LinkID{"ac", "cd", "db", "bd"}, status: StatusCycle, repeat: "D"},
	}
	if !equalShapes(shapes(res), wantShapes) {
		t.Fatalf("path mismatch:\n got %#v\nwant %#v", shapes(res), wantShapes)
	}
}

func equalShapes(got, want []pathShape) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].status != want[i].status || got[i].repeat != want[i].repeat {
			return false
		}
		if len(got[i].nodes) != len(want[i].nodes) || len(got[i].links) != len(want[i].links) {
			return false
		}
		for j := range got[i].nodes {
			if got[i].nodes[j] != want[i].nodes[j] {
				return false
			}
		}
		for j := range got[i].links {
			if got[i].links[j] != want[i].links[j] {
				return false
			}
		}
	}
	return true
}

// TestCycleAndDepthLimitInSameTraversal 同一次遍历中两条路径分别走向
// 环路终态与深度截断终态，二者必须分类清楚。
func TestCycleAndDepthLimitInSameTraversal(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C", "D")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")
	mustAddLink(t, g, "bc", tRel, "B", "C") // 直链 A-B-C-D 在 MaxDepth=3 被截断
	mustAddLink(t, g, "cd", tRel, "C", "D")
	mustAddLink(t, g, "ba", tRel, "B", "A") // A-B-A 真实环路（第一跳之外）

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	counts := statusCounts(res)
	if counts[StatusCycle] != 1 {
		t.Fatalf("want 1 cycle, got %v %#v", counts, shapes(res))
	}
	if counts[StatusDepthLimit] != 1 {
		t.Fatalf("want 1 depth-limited, got %v %#v", counts, shapes(res))
	}
	for _, p := range res.Paths {
		switch p.Status {
		case StatusCycle:
			if p.Cycle.RepeatedObject != "A" {
				t.Fatalf("cycle should close on A, got %q", p.Cycle.RepeatedObject)
			}
		case StatusDepthLimit:
			if p.Depth != 3 || p.Nodes[3] != "D" {
				t.Fatalf("depth-limited path must end on D at depth 3, got depth=%d nodes=%v", p.Depth, p.Nodes)
			}
		}
	}
}

// TestCycleWinsOverDepthAtSameHop 同一跳同时满足环路判定与深度上限：
// MaxDepth=2，路径 A->B 深度 1，B->A 闭合发生在深度 2（恰为上限），
// 必须固定报告为环路而非截断。
func TestCycleWinsOverDepthAtSameHop(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")
	mustAddLink(t, g, "ba", tRel, "B", "A")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := statusCounts(res)
	if counts[StatusCycle] != 1 || counts[StatusDepthLimit] != 0 {
		t.Fatalf("cycle must win over depth limit on the same hop: %v %#v", counts, shapes(res))
	}
}

// TestBoundaryTermination 叶子节点自然终止；无任何链接时起点本身即边界。
func TestBoundaryTermination(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := statusCounts(res)
	if len(counts) != 1 || counts[StatusBoundary] != 1 {
		t.Fatalf("want single boundary path, got %v %#v", counts, shapes(res))
	}
}

// TestValidationErrorPrecedence 三类互斥错误按固定次序只报第一类。
func TestValidationErrorPrecedence(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A")
	mustAddLinkTypes(t, g, tRel)
	logger := NewMemoryLogger()
	svc := NewService(g, logger)

	// 1) 起始对象不存在优先，即使其余问题也同时存在。
	_, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "missing", Directions: map[LinkTypeID]Direction{"ghost": DirOutbound}, MaxDepth: 0,
	})
	if !errors.Is(err, ErrStartObjectNotFound) {
		t.Fatalf("want ErrStartObjectNotFound, got %v", err)
	}

	// 2) 其次方向集合问题（未定义链接类型），即使深度也非法。
	_, err = svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: map[LinkTypeID]Direction{"ghost": DirOutbound}, MaxDepth: 0,
	})
	if !errors.Is(err, ErrInvalidDirections) {
		t.Fatalf("want ErrInvalidDirections, got %v", err)
	}

	// 2b) 方向集合为空。
	_, err = svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: nil, MaxDepth: 3,
	})
	if !errors.Is(err, ErrInvalidDirections) {
		t.Fatalf("want ErrInvalidDirections for empty directions, got %v", err)
	}

	// 2c) 非法方向值。
	_, err = svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: map[LinkTypeID]Direction{tRel: Direction(99)}, MaxDepth: 3,
	})
	if !errors.Is(err, ErrInvalidDirections) {
		t.Fatalf("want ErrInvalidDirections for bad direction value, got %v", err)
	}

	// 3) 最后才是深度问题。
	_, err = svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 0,
	})
	if !errors.Is(err, ErrInvalidDepth) {
		t.Fatalf("want ErrInvalidDepth, got %v", err)
	}

	// 被拒绝的请求不产生任何路径，且仍被日志记录为 rejected。
	for _, e := range logger.Entries() {
		if e.Accepted || len(e.Paths) != 0 {
			t.Fatalf("rejected request must yield no partial paths: %#v", e)
		}
	}
}
