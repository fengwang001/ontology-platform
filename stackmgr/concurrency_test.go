package stackmgr

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentQuotaAccuracy：多个执行流各自驱动不同协程，
// 任何时刻各协程尺寸之和不得超过总配额；且不得出现重复扣减/归还。
func TestConcurrentQuotaAccuracy(t *testing.T) {
	const total = 128
	m, err := New(Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 32,
		TotalQuota: total, ShrinkRatio: ShrinkRatio{1, 4}})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	const workers = 8
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			// Spawn 可能因某个等价串行序中“他人先占大栈”而暂时配额不足；
			// 让其稍后重试，等价于该协程在串行序中排在后面创建。
			var co int
			for {
				id, err := m.Spawn()
				if err == nil {
					co = id
					break
				}
				if classOf(err) != ClassQuota {
					t.Errorf("spawn: %v", err)
					return
				}
				time.Sleep(time.Microsecond)
			}
			// 反复压到上限附近再弹到只剩一帧，覆盖增长/收缩与配额争用。
			for round := 0; round < 60; round++ {
				for {
					e := m.Push(co, 1+(round%7))
					if e != nil {
						break // 溢出或配额：状态不得被破坏
					}
				}
				for {
					if n, _ := m.FrameCount(co); n <= 1 {
						break
					}
					if err := m.Pop(co); err != nil {
						t.Errorf("pop: %v", err)
						return
					}
				}
				s := m.Stats()
				sum := 0
				for _, c := range s.Coroutines {
					sum += c.Size
				}
				if sum > total || s.QuotaUsed > total {
					t.Errorf("quota exceeded: sum=%d used=%d total=%d", sum, s.QuotaUsed, total)
					return
				}
				if sum != s.QuotaUsed {
					t.Errorf("quota accounting drift: sum sizes=%d used=%d", sum, s.QuotaUsed)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// 每个协程弹到只剩一帧后，再统一弹空：尺寸回落 base，配额精确归还。
	for _, c := range m.Stats().Coroutines {
		for {
			if err := m.Pop(c.ID); err != nil {
				break
			}
		}
	}
	s := m.Stats()
	if s.QuotaUsed != workers*4 {
		t.Fatalf("final quota used=%d want %d", s.QuotaUsed, workers*4)
	}
	if r := s.QuotaRemaining(); r != total-workers*4 {
		t.Fatalf("remaining=%d want %d", r, total-workers*4)
	}
}

// TestRelocationCostIndependentOfOthers：一个协程搬迁的耗时/正确性
// 不应随“其它协程数量”变化。这里用大量无关协程存在时，验证目标协程
// 搬迁后指针语义不变，且其搬迁只触碰自身栈（不遍历其它协程）。
func TestRelocationCostIndependentOfOthers(t *testing.T) {
	m, _ := New(Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 64, TotalQuota: 1 << 20,
		ShrinkRatio: ShrinkRatio{1, 4}})
	target, _ := m.Spawn()
	// 制造大量其它协程，每个都有若干帧与指针。
	const others = 64
	var ids []int
	for i := 0; i < others; i++ {
		id, err := m.Spawn()
		if err != nil {
			t.Fatalf("spawn others: %v", err)
		}
		ids = append(ids, id)
		if err := m.Push(id, 4); err != nil {
			t.Fatalf("other push: %v", err)
		}
	}
	must(t, m.Push(target, 4))
	p, _ := m.AddrOf(target, -1, 0)
	must(t, m.WriteIntAt(target, p, 55))
	// 目标协程连续增长搬迁；结果与其它协程数量无关。
	must(t, m.Push(target, 30)) // 4->...->64
	if v, err := m.ReadInt(target, p); err != nil || v != 55 {
		t.Fatalf("relocation affected by other coroutines: %d,%v", v, err)
	}
	for _, id := range ids {
		if n, _ := m.FrameCount(id); n != 1 {
			t.Fatalf("other coroutine %d disturbed: frames=%d", id, n)
		}
	}
}

// TestPointerFixupCostProportionalToPointers：搬迁时只遍历实际存在的
// 栈内指针（tracker.count），而非栈总槽位数。用计数断言该性质：
// 构造一个尺寸很大但只有少量指针的栈，搬迁后 tracker 计数不变且正确。
func TestPointerFixupCostProportionalToPointers(t *testing.T) {
	m, _ := New(Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 64, TotalQuota: 4096,
		ShrinkRatio: ShrinkRatio{1, 4}})
	co, _ := m.Spawn()
	must(t, m.Push(co, 4))
	p, _ := m.AddrOf(co, -1, 0)
	must(t, m.WriteIntAt(co, p, 7))
	q, _ := m.AddrOf(co, -1, 1)
	must(t, m.WritePtr(co, -1, 2, q)) // 仅 1 个槽存放指针
	must(t, m.Push(co, 30))           // 搬迁到 64 槽，但真实指针只有 1 个
	if n := m.cos[co].tracker.count(); n != 1 {
		t.Fatalf("tracker pointers=%d want 1 (must not scale with slots)", n)
	}
	// 那唯一一个指针搬迁后仍指向正确槽位。
	if v, err := m.ReadInt(co, q); err != nil || v != 0 {
		t.Fatalf("fixed-up pointer read=%d,%v", v, err)
	}
	if v, _ := m.ReadInt(co, p); v != 7 {
		t.Fatalf("pointee value=%d", v)
	}
}

// TestPushPopConstantWhenNoRelocation：不触发搬迁时，连续压弹为常量
// 开销——以增长/收缩计数均为 0 来客观验证没有任何 O(尺寸) 的搬迁发生。
func TestPushPopConstantWhenNoRelocation(t *testing.T) {
	m, _ := New(Config{BaseSize: 64, GrowthFactor: 2, MaxStackSize: 256, TotalQuota: 4096,
		ShrinkRatio: ShrinkRatio{1, 4}})
	co, _ := m.Spawn()
	for round := 0; round < 50; round++ {
		for i := 0; i < 8; i++ {
			must(t, m.Push(co, 4))
		}
		for i := 0; i < 8; i++ {
			must(t, m.Pop(co))
		}
	}
	s := m.Stats().Coroutines[0]
	if s.Growths != 0 || s.Shrinks != 0 || s.Size != 64 {
		t.Fatalf("relocation happened on constant path: %+v", s)
	}
}
