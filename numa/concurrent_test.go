package numa

import (
	"fmt"
	"sync"
	"testing"
)

// 同一容器并发 Admit 恰有一次成功；成功后总量记账保持一致。
func TestConcurrentAdmitExactlyOnce(t *testing.T) {
	m, err := NewManager(4, []int64{8, 8, 8, 8}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 64
	var wg sync.WaitGroup
	var success, rejected int64
	var mu sync.Mutex
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := m.Admit("c", 4, nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if err == ErrContainerExists {
				rejected++
			} else {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if success != 1 || rejected != goroutines-1 {
		t.Fatalf("success=%d rejected=%d", success, rejected)
	}
	r, err := m.Query("c")
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, a := range r.Allocation {
		sum += a
	}
	if sum != 4 || r.Mask == 0 || r.Mask&^m.full != 0 {
		t.Fatalf("bad record: %+v", r)
	}
	t.Logf("concurrent admit x%d: exactly 1 success, record=%+v", goroutines, r)
}

// 混合并发 Admit/Release/Query 后，各节点分配总量不超过容量且已登记容器自洽。
func TestConcurrentMixedInvariants(t *testing.T) {
	m, _ := NewManager(3, []int64{16, 16, 16}, PolicyBestEffort)
	const ids = 40
	var wg sync.WaitGroup
	for round := 0; round < 4; round++ {
		for i := 0; i < ids; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := fmt.Sprintf("c%d", i)
				if _, err := m.Admit(id, 3, nil); err != nil && err != ErrContainerExists && err != ErrInsufficientCapacity {
					t.Errorf("admit: %v", err)
				}
				if _, err := m.Query(id); err != nil && err != ErrQueryNotFound {
					t.Errorf("query: %v", err)
				}
			}(i)
		}
		wg.Wait()
		for i := 0; i < ids; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := m.Release(fmt.Sprintf("c%d", i)); err != nil && err != ErrReleaseNotFound {
					t.Errorf("release: %v", err)
				}
			}(i)
		}
		wg.Wait()
	}
	m.mu.Lock()
	totalUsed := make([]int64, m.n)
	for id, r := range m.records {
		var sum int64
		for i, a := range r.alloc {
			sum += a
			totalUsed[i] += a
			if a < 0 {
				t.Errorf("negative alloc %s", id)
			}
		}
		if sum != r.req || r.mask == 0 || r.mask&^m.full != 0 {
			t.Errorf("bad record %s: %+v", id, r)
		}
	}
	for i := range totalUsed {
		if totalUsed[i] > m.caps[i] {
			t.Errorf("node %d over capacity: %d > %d", i, totalUsed[i], m.caps[i])
		}
	}
	m.mu.Unlock()
	t.Log("mixed concurrent admit/query/release: invariants hold")
}

type op struct {
	id   string
	req  int64
	prov []ProviderHints
}

func replay(t *testing.T, seed int, ops []op) []string {
	t.Helper()
	m, err := NewManager(3, []int64{10, 10, 10}, PolicyRestricted)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(ops)*2)
	for _, o := range ops {
		res, err := m.Admit(o.id, o.req, o.prov)
		if err != nil {
			out = append(out, fmt.Sprintf("A:%s:ERR:%v", o.id, err))
			continue
		}
		out = append(out, fmt.Sprintf("A:%s:%03b:%v:%v", o.id, res.Mask, res.Preferred, res.Allocation))
		if err := m.Release(o.id); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("R:%s", o.id))
	}
	return out
}

// 相同的 Admit/Release 序列两次重放结果完全一致。
func TestReplayDeterminism(t *testing.T) {
	ops := []op{
		{"a", 3, nil},
		{"b", 4, []ProviderHints{hs(Hint{Mask: 0b011, Preferred: true})}},
		{"c", 2, []ProviderHints{hp()}},
		{"d", 9, []ProviderHints{hs(Hint{Mask: 0b100, Preferred: true})}},
		{"e", 5, []ProviderHints{hs(), hp()}},
		{"f", 1, nil},
	}
	first := replay(t, 1, ops)
	second := replay(t, 2, ops)
	if len(first) != len(second) {
		t.Fatal("length mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("non-deterministic at %d:\n%s\n%s", i, first[i], second[i])
		}
	}
	t.Logf("deterministic replay trace:\n%v", first)
}
