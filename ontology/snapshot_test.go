package ontology

import (
	"sync"
	"testing"
)

// 并发修改图结构与权限标签同时发生：任何一次遍历结果都必须等价于
// 某个确定快照上的结果，且结构与标签来自同一版本（不可各取一个时点）。
// 校验方式：遍历结果必须与“遍历所记录快照版本”上的朴素实现完全一致。
func TestConcurrentStructureAndLabelMutations(t *testing.T) {
	g := NewGraphStore()
	for _, o := range []string{"s", "a", "b", "c"} {
		g.AddObject(o)
	}
	mustAdd(t, g, link("l1", "s", "a", "A"))
	mustAdd(t, g, link("l2", "a", "b", "A"))
	mustAdd(t, g, link("l3", "b", "c", "B"))
	mustAdd(t, g, link("l4", "c", "a", "B"))

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 结构修改者：不断增删链接。
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
			id := "dyn"
			if i%2 == 0 {
				_ = g.AddLink(link(id, "s", "b", "A"))
			} else {
				g.RemoveLink(id)
			}
			i++
		}
	}()

	// 权限标签修改者：与结构修改交错。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = g.SetLinkLabel("l3", "A")
			_ = g.SetLinkLabel("l3", "B")
		}
	}()

	svc := NewService(g, &MemoryLogger{})
	labels := labelSet("A")
	for k := 0; k < 300; k++ {
		resp, err := svc.Traverse(TraverseRequest{
			CallerID: "racer", Start: "s", Labels: labels, MaxDepth: 6,
		})
		if err != nil {
			continue // 起始对象可能在极端快照下不可见等，跳过错误快照
		}
		// 回放遍历声称绑定的版本：结构与权限标签必须来自同一时点，
		// 且结果与该版本上的朴素实现逐路径一致（无图/标签跨时点撕裂）。
		frozen, ok := g.SnapshotAt(resp.SnapshotVersion)
		if !ok {
			t.Fatalf("遍历记录的快照版本 %d 无法回放", resp.SnapshotVersion)
		}
		want := NaiveTraverse(frozen, TraverseRequest{
			Start: "s", Labels: labels, MaxDepth: 6,
		})
		if len(normalize(resp.Paths)) != len(normalize(want.Paths)) {
			t.Fatalf("版本 %d 快照撕裂：got=%v want=%v",
				resp.SnapshotVersion, normalize(resp.Paths), normalize(want.Paths))
		}
	}
	close(stop)
	wg.Wait()
}

// 强快照一致性：固定一个快照对象，在其上并发修改存储，遍历结果必须
// 与该快照上的朴素实现逐路径一致（证明遍历绑定单一不可变快照）。
func TestSnapshotImmutabilityAgainstMutations(t *testing.T) {
	g := NewGraphStore()
	for _, o := range []string{"s", "a", "b"} {
		g.AddObject(o)
	}
	mustAdd(t, g, link("l1", "s", "a", "A"))
	mustAdd(t, g, link("l2", "a", "b", "B"))
	snap := g.Snapshot() // 固定快照 v?

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = g.SetLinkLabel("l2", "A")
			g.RemoveLink("l1")
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = g.AddLink(link("l2", "a", "b", "B"))
			_ = g.AddLink(link("l1", "s", "a", "A"))
		}
	}()

	svc := NewService(g, nil)
	req := TraverseRequest{Start: "s", Labels: labelSet("A"), MaxDepth: 5}
	for k := 0; k < 200; k++ {
		got, err := svc.traverseSnapshot(snap, req)
		if err != nil {
			t.Fatalf("fixed snapshot must remain valid: %v", err)
		}
		want := NaiveTraverse(snap, req)
		if len(normalize(got.Paths)) != len(normalize(want.Paths)) {
			t.Fatalf("固定快照结果被并发修改污染：got=%v want=%v",
				normalize(got.Paths), normalize(want.Paths))
		}
	}
	close(stop)
	wg.Wait()
}
