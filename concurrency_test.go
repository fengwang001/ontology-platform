package ontology

import (
	"sync"
	"testing"
)

// 并发创建/删除/权限变更：最终账本必须保持自洽（两端槽位计数与现存链接一致），
// 即所有操作等价于按某个全局串行顺序逐一应用。
func TestConcurrentOpsAreSerializable(t *testing.T) {
	p := testWorld(t)
	actors := []string{"alice", "bob", "carol"}

	var wg sync.WaitGroup
	for round := 0; round < 40; round++ {
		for _, a := range actors {
			wg.Add(3)
			a := a
			go func() {
				defer wg.Done()
				p.GrantActor(a, "d1", "docRef", Visible)
			}()
			go func() {
				defer wg.Done()
				p.GrantActor(a, "p1", "personRef", Visible)
			}()
			go func() {
				defer wg.Done()
				link := Link{LinkType: "authored", SourceInstance: "d1", TargetInstance: "p1"}
				if err := p.CreateLink(a, "authored", "d1", "p1"); err == nil {
					_ = p.DeleteLink(a, "authored", "d1", "p1")
				}
				_ = link
			}()
		}
	}
	wg.Wait()

	// 不变量 1：起点/终点槽位计数都等于现存链接数（0 或 1，因为同一唯一键）。
	n := len(p.AllLinks())
	if got := p.SourceCount("authored", "d1"); got != n {
		t.Fatalf("source slot count %d != physical links %d", got, n)
	}
	if got := p.TargetCount("authored", "p1"); got != n {
		t.Fatalf("target slot count %d != physical links %d", got, n)
	}
	if n > 1 {
		t.Fatalf("identical link key can only exist at most once, got %d", n)
	}

	// 不变量 2：基数上限为 1 的串行化结果里，最终若链接存在则计数恰为 1。
	if n == 1 && (p.SourceCount("authored", "d1") != 1 || p.TargetCount("authored", "p1") != 1) {
		t.Fatalf("cardinality bookkeeping inconsistent after concurrent ops")
	}
	t.Logf("[concurrency] 360 goroutines applied; final physical links=%d, slots(src=%d tgt=%d)",
		n, p.SourceCount("authored", "d1"), p.TargetCount("authored", "p1"))
}
