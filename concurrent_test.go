package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentAccess 并发调用全部方法，配合 go test -race 检查数据竞争；
// 运行结束后状态仍须满足相容与祖先意向不变量。
func TestConcurrentAccess(t *testing.T) {
	m := NewLockManager()
	if err := m.Register("root", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := m.Register(id, "root"); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 12
	var wg sync.WaitGroup
	nodes := []string{"root", "a", "b", "c"}

	for w := 0; w < workers; w++ {
		txn := int64(w + 1)
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			// 每个事务先建立祖先意向，随后在各节点上循环操作。
			_, _ = m.Lock(txn, "root", IX)
			for i := 0; i < 300; i++ {
				node := nodes[int((seed+int64(i))%int64(len(nodes)))]
				mode := allModes[int((seed+int64(i))%int64(len(allModes)))]
				if _, err := m.Lock(txn, node, mode); err != nil {
					// 被拒绝（冲突等）属正常：状态不应被改变。
					_, _, _ = m.Held(txn, node)
				}
				_, _, _ = m.Held(txn, node)
				_, _ = m.Holders(node)
				if i%7 == 0 {
					// 叶节点可安全释放；非叶节点可能因后代锁被拒。
					_ = m.Unlock(txn, "a")
					_ = m.Unlock(txn, "b")
					_ = m.Unlock(txn, "c")
				}
			}
			_ = m.ReleaseAll(txn)
		}(int64(w))
	}
	wg.Wait()

	checkInvariants(t, m, 0)

	for _, node := range nodes {
		holders, err := m.Holders(node)
		if err != nil {
			t.Fatal(err)
		}
		if len(holders) != 0 {
			t.Fatalf("ReleaseAll 后节点 %q 应无持锁，实际 %v", node, holders)
		}
	}
}
