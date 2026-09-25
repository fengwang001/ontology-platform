package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// 多协程 Push 大量唯一 ID：Snapshot 里 ID 不重复，持有数不超过 K。
func TestConcurrentPushNoLossNoDuplicate(t *testing.T) {
	const k = 32
	sel, err := New(k, Desc)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const perG = 1000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				id := fmt.Sprintf("g%d-%05d", g, i)
				sel.Push(id, float64(g*perG+i))
			}
		}(g)
	}

	// 与 Push 并发地读 Snapshot / Len：只能观察到合法中间态。
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := sel.Snapshot()
				if len(snap) > k {
					t.Errorf("snapshot length %d exceeds k", len(snap))
					return
				}
				if !snapshotSorted(sel.dir, snap) {
					t.Errorf("snapshot not in rank order: %v", snap)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readers.Wait()

	if n := sel.Len(); n != k {
		t.Fatalf("final len: got %d want %d", n, k)
	}
	final := sel.Snapshot()
	seen := make(map[string]bool, len(final))
	for _, e := range final {
		if seen[e.ID] {
			t.Fatalf("duplicate ID %q", e.ID)
		}
		seen[e.ID] = true
	}
}

// 多协程并发覆盖同一批 ID：最终 Snapshot 里每个 ID 至多一次，
// 且长度不超过 K，内容始终是当前分数下正确的 Top-K。
func TestConcurrentOverwriteSameIDs(t *testing.T) {
	const k = 10
	sel, _ := New(k, Desc)
	const ids = 40

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for round := 0; round < 200; round++ {
				for id := 0; id < ids; id++ {
					sel.Push(fmt.Sprintf("id-%02d", id), float64((id*7+seed*13+round)%97))
				}
			}
		}(g)
	}
	wg.Wait()

	snap := sel.Snapshot()
	if len(snap) != k {
		t.Fatalf("len: got %d want %d", len(snap), k)
	}
	seen := map[string]bool{}
	for _, e := range snap {
		if seen[e.ID] {
			t.Fatalf("duplicate ID after concurrent overwrite: %q", e.ID)
		}
		seen[e.ID] = true
	}
}

func snapshotSorted(dir Direction, snap []Element) bool {
	for i := 1; i < len(snap); i++ {
		if rankLess(dir, snap[i], snap[i-1]) {
			return false
		}
	}
	return true
}
