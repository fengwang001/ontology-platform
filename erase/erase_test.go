package erase_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/erase"
)

func TestNewParamValidation(t *testing.T) {
	for _, tc := range []struct {
		s int
		T int64
	}{
		{0, 1}, {9, 1}, {1, 0}, {1, 1_000_000_001}, {-1, -1},
	} {
		if _, err := erase.New(tc.s, tc.T); err != erase.ErrParam {
			t.Fatalf("New(%d,%d) = %v, want ErrParam", tc.s, tc.T, err)
		}
	}
	if _, err := erase.New(8, 1_000_000_000); err != nil {
		t.Fatalf("New(8,1e9) = %v, want nil", err)
	}
}

// 非导出计数器：Overdue 检视的擦除单数不超过返回条数加 1，
// 与 Active 总数及已 Done 的擦除单数无关。
func TestOverdueInspectionBound(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		l, err := erase.New(1, 100)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := l.Request(erase.RolePrivacy, int64(i+1), int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		// 让前一半擦除单 Done（已 Done 的数量不得影响检视数）。
		for i := 1; i <= n/2; i++ {
			if err := l.Ack(erase.RoleOps, i, 1, int64(n-1)); err != nil {
				t.Fatal(err)
			}
		}
		// 剩余 Active 单 deadline = 100+i（i 从 n/2 起），取 now 使恰有 k 条逾期。
		k := n / 4
		now := int64(100 + n/2 + k - 1)
		got := l.Overdue(now)
		if len(got) != k {
			t.Fatalf("n=%d: |Overdue| = %d, want %d", n, len(got), k)
		}
		if insp := l.OverdueInspected(); insp > int64(len(got)+1) {
			t.Fatalf("n=%d: inspected = %d, want <= %d", n, insp, len(got)+1)
		}
		// 全部未逾期时检视数不超过 1。
		if got := l.Overdue(0); len(got) != 0 {
			t.Fatalf("n=%d: Overdue(0) 非空", n)
		}
		if insp := l.OverdueInspected(); insp > 1 {
			t.Fatalf("n=%d: inspected = %d, want <= 1", n, insp)
		}
	}
}

// Overdue 只读：不做时钟检查，也不推进最大 now。
func TestOverdueReadOnly(t *testing.T) {
	l, _ := erase.New(1, 100)
	if _, err := l.Request(erase.RolePrivacy, 7, 50); err != nil {
		t.Fatal(err)
	}
	// now 小于最大 now（50）也允许，且不推进、不报错。
	if got := l.Overdue(10); len(got) != 0 {
		t.Fatalf("Overdue(10) = %v, want empty", got)
	}
	if got := l.Overdue(150); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("Overdue(150) = %v, want [e1]", got)
	}
	// 最大 now 未被 Overdue 污染：now=50 的写操作仍被接受。
	if err := l.Ack(erase.RoleOps, 1, 1, 50); err != nil {
		t.Fatalf("Ack at old now rejected: %v", err)
	}
	// 时钟回退仍被拒绝。
	if err := l.Ack(erase.RoleOps, 1, 1, 49); err != erase.ErrClock {
		t.Fatalf("Ack(49) = %v, want ErrClock", err)
	}
}

// 并发调用等价于某个串行顺序：-race 下无数据竞争，且最终不变式成立。
func TestConcurrentSerialization(t *testing.T) {
	const workers = 8
	l, _ := erase.New(4, 1_000_000)
	var clock atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := clock.Add(1)
				subject := int64(w*200 + i + 1)
				id, err := l.Request(erase.RolePrivacy, subject, now)
				if err != nil {
					continue
				}
				for s := 1; s <= 4; s++ {
					_ = l.Ack(erase.RoleOps, id, s, clock.Add(1))
				}
				_ = l.Overdue(clock.Load())
			}
		}(w)
	}
	wg.Wait()
	// 不变式：Done 的擦除单对每个系统都有 ack，Deferred 无任何 ack。
	err := l.View(func(c *erase.Core) error {
		for i := 1; i <= c.NumErasures(); i++ {
			e := c.Erasure(i)
			for _, a := range e.AckAt {
				if e.Status == erase.Done && a < 0 {
					t.Errorf("e%d Done 但存在待确认系统", e.ID)
				}
				if e.Status == erase.Deferred && a >= 0 {
					t.Errorf("e%d Deferred 但已有 ack", e.ID)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
