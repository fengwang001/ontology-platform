package ontology

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSnapshotIsolation 验证并发修改图结构与权限标签时，
// 每次遍历的结果都等价于在某个确定快照上执行的结果，
// 且两类修改共享同一个快照时点。
func TestSnapshotIsolation(t *testing.T) {
	store := buildStore(t,
		Link{ID: "e0", From: "A", To: "B", Label: "L0"},
		Link{ID: "e1", From: "B", To: "C", Label: "L1"},
		Link{ID: "e2", From: "C", To: "A", Label: "L2"},
		Link{ID: "e3", From: "C", To: "D", Label: "L0"},
	)
	req := Request{CallerLabels: []Label{"L0", "L1"}, Start: "A", MaxDepth: 8}

	// 变更序列：交替修改图结构与权限标签。
	type mutation struct {
		apply func() error
	}
	mutations := []mutation{
		{func() error { return store.SetLinkLabel("e2", "L1") }},                                 // 标签：e2 变为可见
		{func() error { return store.AddLink(Link{ID: "e4", From: "D", To: "B", Label: "L0"}) }}, // 结构：新增成环链接
		{func() error { return store.SetLinkLabel("e0", "L9") }},                                 // 标签：e0 变为不可见
		{func() error { store.RemoveLink("e1"); return nil }},                                    // 结构：删除链接
		{func() error { return store.SetLinkLabel("e3", "L1") }},                                 // 标签
		{func() error { return store.AddLink(Link{ID: "e5", From: "A", To: "A", Label: "L1"}) }}, // 结构：自环
		{func() error { return store.SetLinkLabel("e5", "L7") }},                                 // 标签：自环隐藏
		{func() error { store.RemoveLink("e2"); return nil }},                                    // 结构
	}

	// 记录每一个已提交状态对应的快照（S0 为初始状态）。
	snaps := []*Snapshot{store.Snapshot()}

	var mu sync.Mutex
	results := make(map[string]int) // render -> count
	versions := make(map[uint64]bool)

	var completed atomic.Int64
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res, err := Traverse(store, req, nil)
				if err != nil {
					// 起始对象可能在某些快照上不可见，错误本身也是合法结果。
					mu.Lock()
					results["ERR:"+err.Error()]++
					mu.Unlock()
					completed.Add(1)
					continue
				}
				mu.Lock()
				results[render(res.Root)]++
				versions[res.Version] = true
				mu.Unlock()
				completed.Add(1)
			}
		}()
	}
	// 先确认读者已经开始产出结果，再逐步施加变更，
	// 每次变更之间让出调度，保证遍历能落在不同的已提交状态上。
	waitResults := func(atLeast int64) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for completed.Load() < atLeast {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %d traversal results, got %d",
					atLeast, completed.Load())
			}
			runtime.Gosched()
		}
	}
	waitResults(16)
	for _, m := range mutations {
		if err := m.apply(); err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, store.Snapshot())
		runtime.Gosched()
	}
	waitResults(64)
	close(stop)
	readers.Wait()

	if len(results) == 0 {
		t.Fatal("no traversal results collected")
	}
	// 预先计算每个已提交快照上的合法结果（含错误情形）。
	legal := make(map[string]bool)
	legalVersions := make(map[uint64]bool)
	for _, snap := range snaps {
		res, err := snap.Traverse(req, nil)
		if err != nil {
			legal["ERR:"+err.Error()] = true
			continue
		}
		legal[render(res.Root)] = true
		legalVersions[snap.Version()] = true
	}
	// 每个并发遍历结果都必须等价于某个确定快照上的执行结果。
	for r := range results {
		if !legal[r] {
			t.Fatalf("traversal result matches no committed snapshot:\n%s", r)
		}
	}
	// 结果携带的快照版本必须真实对应某个已提交状态：
	// 图结构与权限标签取自同一时点。
	for v := range versions {
		if !legalVersions[v] {
			t.Fatalf("traversal ran on uncommitted snapshot version %d", v)
		}
	}
	t.Logf("observed %d distinct results across %d committed snapshots", len(results), len(snaps))
}

// TestSnapshotImmutable 验证遍历开始后的并发修改不影响已取出的快照。
func TestSnapshotImmutable(t *testing.T) {
	store := buildStore(t,
		Link{ID: "e0", From: "A", To: "B", Label: "L0"},
		Link{ID: "e1", From: "B", To: "A", Label: "L0"},
	)
	req := Request{CallerLabels: []Label{"L0"}, Start: "A", MaxDepth: 5}
	snap := store.Snapshot()
	before, err := snap.Traverse(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 并发修改图结构与标签。
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = store.AddLink(Link{ID: fmt.Sprintf("x%d", i), From: "A", To: "B", Label: "L9"})
			_ = store.SetLinkLabel("e0", Label(fmt.Sprintf("L%d", i%3)))
			store.RemoveLink(fmt.Sprintf("x%d", i))
		}(i)
	}
	wg.Wait()
	after, err := snap.Traverse(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if render(before.Root) != render(after.Root) {
		t.Fatalf("snapshot mutated by concurrent writes:\nbefore:\n%s\nafter:\n%s",
			render(before.Root), render(after.Root))
	}
}
