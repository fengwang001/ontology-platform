package tlb

import (
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrent 并发调用所有操作：结果必须等价于某个串行顺序
// （由单一互斥锁保证），回绕对外是原子步骤。配合 -race 运行。
func TestConcurrent(t *testing.T) {
	cfg := Config{ASIDs: 8, CPUs: 4, TLBCap: 4, MaxMMs: 64}
	m := mustNew(t, cfg)
	mustCreateMMs(t, m, 16)
	const workers = 8
	const opsEach = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < opsEach; i++ {
				cpu := r.Intn(cfg.CPUs)
				mm := r.Intn(24)
				switch r.Intn(6) {
				case 0:
					_, _, _, _, _ = m.Switch(cpu, mm)
				case 1:
					_, _, _ = m.Fill(cpu, uint64(r.Intn(16)), uint64(r.Intn(100)))
				case 2:
					_, _, _ = m.Lookup(cpu, uint64(r.Intn(16)))
				case 3:
					_, _ = m.Invalidate(mm, uint64(r.Intn(16)))
				case 4:
					_, _, _ = m.DestroyMM(mm)
				case 5:
					_, _ = m.CreateMM()
				}
			}
		}(int64(w)*977 + 1)
	}
	wg.Wait()
	// 并发结束后验证不变量。
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gen < 1 {
		t.Fatalf("G=%d 应 >= 1", m.gen)
	}
	for cpu := range m.cpus {
		if n := m.cpus[cpu].tlb.ll.Len(); n > cfg.TLBCap {
			t.Fatalf("TLB(%d) 条目数 %d 超过容量 %d", cpu, n, cfg.TLBCap)
		}
		if n := len(m.cpus[cpu].tlb.ents); n != m.cpus[cpu].tlb.ll.Len() {
			t.Fatalf("TLB(%d) 索引与链表不一致", cpu)
		}
	}
	// 同一世代内不同存活 mm 的 asid 互不相同。
	seen := make(map[uint32]int)
	for i, e := range m.mms {
		if !e.alive || e.asid == 0 || e.gen != m.gen {
			continue
		}
		if j, dup := seen[e.asid]; dup {
			t.Fatalf("mm%d 与 mm%d 同世代共享 asid %d", j, i, e.asid)
		}
		seen[e.asid] = i
	}
	// taken 恰好 = 当前世代已分配未销毁的 asid ∪ 保留的 asid。
	want := make(map[uint32]bool)
	for _, e := range m.mms {
		if e.alive && e.asid != 0 && e.gen == m.gen {
			want[e.asid] = true
		}
	}
	for cpu := range m.cpus {
		if r := m.cpus[cpu].reserved; r.ASID != 0 {
			want[r.ASID] = true
		}
	}
	for a := 1; a <= cfg.ASIDs; a++ {
		if got := m.alloc.taken(uint32(a)); got != want[uint32(a)] {
			t.Fatalf("taken(%d)=%v, want %v", a, got, want[uint32(a)])
		}
	}
	// 条目的 asid 未占用时，该 CPU 必然处于待刷新状态（惰性刷新窗口）。
	for cpu := range m.cpus {
		for _, e := range m.cpus[cpu].tlb.entries() {
			if !m.alloc.taken(e.Key.ASID) && !m.cpus[cpu].pending {
				t.Fatalf("TLB(%d) 含未占用 asid 的条目 %v 且 pending=false", cpu, e)
			}
		}
	}
}
