package thinpool

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentWritesSerializable 在多 goroutine 高竞争下验证：
// 1) 每个虚拟块只产生一次物理分配（幂等映射）；
// 2) 任意接受操作后 totalDeficit <= free 的不变量；
// 3) 已分配总量不超过物理块；
// 4) 事件序号从 1 连续，水位档位与事件发生时的已分配量自洽。
func TestConcurrentWritesSerializable(t *testing.T) {
	p := testPool(t, 40, 400, 25, 75)
	mustCreate(t, p, "a", 50, 8)
	mustCreate(t, p, "b", 50, 8)
	mustCreate(t, p, "c", 50, 8)

	const writers = 8
	const perWriter = 200
	var wg sync.WaitGroup
	allocCounts := make([]int, writers)
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				name := []string{"a", "b", "c"}[(w+i)%3]
				block := (w*7 + i*3) % 50
				got, err := p.Write(name, block)
				if err != nil {
					if pe, ok := err.(*Error); !ok || pe.Kind != KindPoolExhausted {
						t.Errorf("unexpected error: %v", err)
						return
					}
				}
				if got {
					allocCounts[w]++
				}
				s := p.Snapshot()
				if s.TotalDeficit > s.Free || s.Allocated > s.Physical {
					t.Errorf("invariant broken: %+v", s)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	s := p.Snapshot()
	mappedTotal := 0
	for _, vs := range s.Volumes {
		mappedTotal += vs.Allocated
		if vs.Deficit != maxInt(vs.Reservation-vs.Allocated, 0) {
			t.Fatalf("deficit mismatch: %+v", vs)
		}
	}
	if s.Allocated != mappedTotal {
		t.Fatalf("allocation accounting: snap=%d sum=%d", s.Allocated, mappedTotal)
	}

	evs := p.Events()
	for i, e := range evs {
		if e.Seq != i+1 {
			t.Fatalf("event seq gap: %+v", e)
		}
		if e.To != classify(e.Allocated, s.Physical, p.cfg.WarningPct, p.cfg.CriticalPct) {
			t.Fatalf("event level inconsistent: %+v", e)
		}
	}
	totalNew := 0
	for _, c := range allocCounts {
		totalNew += c
	}
	if totalNew != s.Allocated {
		t.Fatalf("reported new allocations=%d but pool allocated=%d", totalNew, s.Allocated)
	}
}

// TestMixedConcurrentOpsSerializable 混合所有操作并发执行，全程检查不变量。
func TestMixedConcurrentOpsSerializable(t *testing.T) {
	p := testPool(t, 30, 400, 30, 80)
	mustCreate(t, p, "a", 40, 5)
	mustCreate(t, p, "b", 40, 5)

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i <= 300; i++ {
				switch (g + i) % 6 {
				case 0:
					_, _ = p.Write("a", i%40)
				case 1:
					_, _ = p.Write("b", i%40)
				case 2:
					_, _ = p.Reclaim("a", (i*2)%40, 1)
				case 3:
					_ = p.SetReservation("b", i%6)
				case 4:
					_ = p.Resize("a", 40)
				case 5:
					name := fmt.Sprintf("tmp%d", g%2)
					if err := p.CreateVolume(name, 10, 1); err == nil {
						_ = p.DeleteVolume(name)
					}
				}
				s := p.Snapshot()
				if s.TotalDeficit > s.Free || s.TotalReserve > s.Physical ||
					s.TotalVirtual*100 > s.Physical*p.cfg.OvercommitPct {
					errCh <- fmt.Errorf("invariant broken: %+v", s)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatal(e)
	}
}

// TestDeterministicReplay 相同操作序列在两个独立池上重放，
// 记账与事件序列必须完全相同。
func TestDeterministicReplay(t *testing.T) {
	seq := []op{
		{"create", "a", 30, 4},
		{"create", "b", 30, 4},
		{"write", "a", 0, 0},
		{"write", "a", 1, 0},
		{"write", "b", 0, 0},
		{"reclaim", "a", 0, 2},
		{"write", "a", 0, 0},
		{"setres", "a", 6, 0},
		{"resize", "a", 35, 0},
		{"delete", "b", 0, 0},
		{"create", "b", 10, 0},
	}
	cfg := Config{PhysicalBlocks: 20, OvercommitPct: 400, WarningPct: 20, CriticalPct: 40}
	p1, _ := New(cfg)
	p2, _ := New(cfg)
	for _, o := range seq {
		_, _, _, _ = runOnPool(p1, o)
		_, _, _, _ = runOnPool(p2, o)
	}
	s1, s2 := p1.Snapshot(), p2.Snapshot()
	if !snapEqual(s1, s2) {
		t.Fatalf("replay snapshots differ\n%+v\n%+v", s1, s2)
	}
	m1, m2 := p1.Events(), p2.Events()
	if len(m1) != len(m2) {
		t.Fatalf("event lengths %d vs %d", len(m1), len(m2))
	}
	for i := range m1 {
		if m1[i] != m2[i] {
			t.Fatalf("event %d differs: %+v vs %+v", i, m1[i], m2[i])
		}
	}
}

func snapEqual(a, b PoolSnapshot) bool {
	if a.Physical != b.Physical || a.Allocated != b.Allocated || a.Free != b.Free ||
		a.TotalVirtual != b.TotalVirtual || a.TotalReserve != b.TotalReserve ||
		a.TotalDeficit != b.TotalDeficit || a.Level != b.Level ||
		len(a.Volumes) != len(b.Volumes) {
		return false
	}
	for i := range a.Volumes {
		if a.Volumes[i] != b.Volumes[i] {
			return false
		}
	}
	return true
}
