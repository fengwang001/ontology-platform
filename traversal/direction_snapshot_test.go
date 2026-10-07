package traversal

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestInboundAndBoth 反向与双向方向集合的基本语义。
func TestInboundAndBoth(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C", "X", "Y")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ba", tRel, "B", "A") // 存储方向 B->A，从 A 反向可到达 B
	mustAddLink(t, g, "ac", tRel, "A", "C") // 存储方向 A->C
	mustAddLink(t, g, "xb", tRel, "X", "B") // B 的入边邻居 X（叶子）
	mustAddLink(t, g, "cy", tRel, "C", "Y") // C 的出边邻居 Y（叶子）

	svc := NewService(g, nil)

	// 仅反向：A<-B，沿入边到达 B；A->C 不允许。
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirInbound), MaxDepth: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []pathShape{
		{nodes: []ObjectID{"A", "B", "X"}, links: []LinkID{"ba", "xb"}, status: StatusBoundary},
	}
	if !equalShapes(shapes(res), want) {
		t.Fatalf("inbound mismatch: %#v", shapes(res))
	}

	// 双向：两个方向各自独立扩展。
	res, err = svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirBoth), MaxDepth: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 双向允许时，沿同一条边返回上一跳即闭合为环（4 条环路），
	// 包括 B-X-B、C-Y-C 这类沿平行方向的往返。
	wantBoth := []pathShape{
		{nodes: []ObjectID{"A", "C", "Y", "C"}, links: []LinkID{"ac", "cy", "cy"}, status: StatusCycle, repeat: "C"},
		{nodes: []ObjectID{"A", "C", "A"}, links: []LinkID{"ac", "ac"}, status: StatusCycle, repeat: "A"},
		{nodes: []ObjectID{"A", "B", "A"}, links: []LinkID{"ba", "ba"}, status: StatusCycle, repeat: "A"},
		{nodes: []ObjectID{"A", "B", "X", "B"}, links: []LinkID{"ba", "xb", "xb"}, status: StatusCycle, repeat: "B"},
	}
	if !equalShapes(shapes(res), wantBoth) {
		t.Fatalf("both mismatch:\n got %#v\nwant %#v", shapes(res), wantBoth)
	}
}

// TestMultipleLinkTypes 不同链接类型的平行链接彼此独立，且可按类型分别开关方向。
func TestMultipleLinkTypes(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B")
	mustAddLinkTypes(t, g, "t1", "t2")
	mustAddLink(t, g, "l1", "t1", "A", "B")
	mustAddLink(t, g, "l2", "t2", "A", "B")

	svc := NewService(g, nil)
	res, err := svc.Traverse(context.Background(), TraversalRequest{
		Start:      "A",
		Directions: map[LinkTypeID]Direction{"t1": DirOutbound},
		MaxDepth:   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1 || res.Paths[0].Links[0].Type != "t1" {
		t.Fatalf("only t1 links should be traversed: %#v", shapes(res))
	}
}

// TestSnapshotIsolationUnderConcurrentMutation 遍历进行期间持续增删链接，
// 结果必须恰好等价于某个已发布快照上的遍历结果，无重复、无遗漏、无混入。
func TestSnapshotIsolationUnderConcurrentMutation(t *testing.T) {
	g := buildStableGraph(t)
	enableSnapshotHistory(g)

	svc := NewService(g, NewMemoryLogger())
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 修改方：不断添加并删除一条会制造额外环路的链接。
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			id := LinkID(fmt.Sprintf("mut-%d", i))
			_ = g.AddLink(Link{ID: id, Type: tRel, Source: "C", Target: "B"})
			g.DeleteLink(id)
			i++
		}
	}()

	// 遍历方：并发执行多轮，每轮结果必须与某个已发布快照的结果逐路径一致。
	for round := 0; round < 200; round++ {
		res, err := svc.Traverse(context.Background(), TraversalRequest{
			Start: "A", Directions: dirs(DirBoth), MaxDepth: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		pinned, err := TraverseSnapshot(snapshotAtVersion(g, res.SnapshotVersion), TraversalRequest{
			Start: "A", Directions: dirs(DirBoth), MaxDepth: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflectResultsEqual(res, pinned) {
			t.Fatalf("round %d result does not match its declared snapshot: %#v", round, shapes(res))
		}
	}
	close(stop)
	wg.Wait()
}

// TestSnapshotExactEquivalence 顺序修改下，遍历结果与其报告版本的快照
// 直接遍历结果完全一致，作为快照等价性的确定性验证。
func TestSnapshotExactEquivalence(t *testing.T) {
	g := buildStableGraph(t)
	svc := NewService(g, nil)

	for i := 0; i < 10; i++ {
		id := LinkID(fmt.Sprintf("mut-%d", i))
		if err := g.AddLink(Link{ID: id, Type: tRel, Source: "C", Target: "A"}); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Traverse(context.Background(), TraversalRequest{
			Start: "A", Directions: dirs(DirBoth), MaxDepth: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		pinned, err := TraverseSnapshot(snapshotAtVersion(g, res.SnapshotVersion), TraversalRequest{
			Start: "A", Directions: dirs(DirBoth), MaxDepth: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		assertResultsEqual(t, res, pinned)
		g.DeleteLink(id)
	}
}

func buildStableGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C", "D")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")
	mustAddLink(t, g, "bc", tRel, "B", "C")
	mustAddLink(t, g, "cd", tRel, "C", "D")
	return g
}
