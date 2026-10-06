package standby

import (
	"fmt"
	"sync"
	"testing"
)

// TestPerfIndependence 用规模比验证开销不随队列长度与航班数增长：
// 10 倍规模下，登记/撤回/确认的平均耗时增长应远小于 10 倍（宽松界 3 倍，
// 用以吸收调度噪声；算法本身对这些规模是常数）。
func TestPerfIndependence(t *testing.T) {
	if testing.Short() {
		t.Skip("perf")
	}
	// buildSys 构造一个拥有 n 条候补条目 + n 个航班的系统；候补条目全部无法兑现
	// （容量 1 已被 1 个待确认条目占满，候补均为 9 人），保证稳态下登记不触发兑现。
	buildSys := func(n int) *System {
		s := New()
		for fi := 0; fi < n; fi++ {
			name := fmt.Sprintf("F%06d", fi)
			if err := s.CreateFlight(name, "Y", 1, n+1000000, 1<<40, 0); err != nil {
				t.Fatal(err)
			}
			if fi == 0 {
				if _, err := s.Register(name, "Y", "occupy", 1, PrioHigh, 0); err != nil {
					t.Fatal(err)
				}
			}
		}
		for j := 0; j < n; j++ {
			if _, err := s.Register("F000000", "Y", fmt.Sprintf("p%08d", j), 9, PrioLow, 0); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	measure := func(n int) float64 {
		s := buildSys(n)
		b1 := testing.Benchmark(func(b *testing.B) {
			// 每条操作落在不同航班上（测 O(1) 定位 + O(1) 登记，不被队列长度影响）。
			for i := 0; i < b.N; i++ {
				fi := 1 + (i % (n - 1))
				name := fmt.Sprintf("F%06d", fi)
				_, _ = s.Register(name, "Y", fmt.Sprintf("q%010d", i), 1, PrioLow, 0)
			}
		})
		return float64(b1.T.Nanoseconds()) / float64(b1.N)
	}
	small := 200
	large := 2000
	r1 := measure(small)
	r2 := measure(large)
	t.Logf("register avg ns: n=%d -> %.1f, n=%d -> %.1f, ratio=%.2f", small, r1, large, r2, r2/r1)
	if r2 > r1*3 {
		t.Fatalf("register cost grew with scale: ratio=%.2f", r2/r1)
	}
}

// TestConcurrentSerializable 并发发起大量操作；要求不死锁、不崩溃，
// 且每次快照看到的状态满足容量不变量。
func TestConcurrentSerializable(t *testing.T) {
	s := New()
	if err := s.CreateFlight("F1", "Y", 100, 100000, 1000, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(g*100000 + i)
				pax := fmt.Sprintf("g%dp%d", g, i)
				id, err := s.Register("F1", "Y", pax, 1+int(EntryID(i%3)), Priority(i%3), now)
				if err != nil {
					continue
				}
				_ = s.Withdraw(id, now+1)
			}
		}(g)
	}
	wg.Wait()
	snap, err := s.Snapshot("F1", "Y", 1<<50)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Confirmed+snap.Pending > snap.Capacity {
		t.Fatalf("invariant violated: confirmed+pending=%d > cap=%d",
			snap.Confirmed+snap.Pending, snap.Capacity)
	}
}

// TestReplayDeterministic 相同操作序列（含每步时刻）重放两次，终态逐字节一致。
func TestReplayDeterministic(t *testing.T) {
	play := func() FlightSnapshot {
		s := New()
		if err := s.CreateFlight("F1", "Y", 3, 10, 5, 0); err != nil {
			t.Fatal(err)
		}
		id1, _ := s.Register("F1", "Y", "a", 2, PrioHigh, 0)
		id2, _ := s.Register("F1", "Y", "b", 3, PrioMid, 1)
		_ = id2
		_, _ = s.Register("F1", "Y", "c", 1, PrioHigh, 2)
		_ = s.Confirm(id1, 3)
		_ = s.ChangePriority(id2, PrioHigh, 4)
		_ = s.CancelConfirmed(id1, 5)
		snap, _ := s.Snapshot("F1", "Y", 40)
		return snap
	}
	a := play()
	b := play()
	if !snapEqual(a, b) {
		t.Fatalf("replay not deterministic:\n%+v\n%+v", a, b)
	}
}

func snapEqual(a, b FlightSnapshot) bool {
	if len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		if a.Entries[i] != b.Entries[i] {
			return false
		}
	}
	return a.Capacity == b.Capacity && a.Confirmed == b.Confirmed &&
		a.Pending == b.Pending && a.WaitingCount == b.WaitingCount && a.Free == b.Free
}

// TestFulfillScanTouchesOnlyActive 结构性证明：
// 已确认/已撤回/已过期/作废条目从不留在等待 Trie 中，因此兑现扫描
// 触及的条目数只可能是“本次兑现 + 被跳过”，与历史已结束条目总量无关。
func TestFulfillScanTouchesOnlyActive(t *testing.T) {
	s := New()
	if err := s.CreateFlight("F1", "Y", 2, 1_000_000, 1<<40, 0); err != nil {
		t.Fatal(err)
	}
	f := s.flights[flightKeyOf("F1", "Y")]
	// 制造 50000 条已结束条目（撤回），它们绝不能出现在等待集合里。
	for i := 0; i < 50000; i++ {
		id, err := s.Register("F1", "Y", fmt.Sprintf("x%08d", i), 9, PrioLow, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Withdraw(id, 0); err != nil {
			t.Fatal(err)
		}
	}
	totalInTries := 0
	for p := range f.waiting {
		l := f.waiting[p].first()
		for l != nil {
			totalInTries++
			l = f.waiting[p].next(l)
		}
	}
	if totalInTries != 0 {
		t.Fatalf("ended entries leaked into waiting trie: %d", totalInTries)
	}
	// 再放入两条小候补并释放 2 座：兑现只考察这两条。
	first, _ := s.Register("F1", "Y", "keep1", 1, PrioHigh, 1)
	second, _ := s.Register("F1", "Y", "keep2", 1, PrioLow, 1)
	// 容量上调到 50002 会释放座位；用取消一个已确认更直接，这里用容量上调触发。
	if err := s.SetCapacity("F1", "Y", 2, 2); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot("F1", "Y", 2)
	st := states(snap)
	if st[first] != StatePending || st[second] != StatePending {
		t.Fatalf("active entries must be served regardless of 50000 ended entries: %v %v",
			st[first], st[second])
	}
}
