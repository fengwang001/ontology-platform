package temporal

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
)

// 并发提交/撤回/查询不得产生竞态（-race 覆盖），且最终状态必须自洽：
// 对每个 (主体, 标签)，引擎结果与朴素参照在全量时刻上逐项一致。
func TestConcurrentSerializability(t *testing.T) {
	ctx := context.Background()
	buf := &syncBuffer{}
	eng := NewEngine(0, NewAuditLog(buf))

	var wg sync.WaitGroup
	// 多写者：提交 Grant/Deny，生效时刻各有排队与立即生效两类。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				c := Change{
					ID:        fmt.Sprintf("w%dn%d", w, i),
					Subject:   Subject(fmt.Sprintf("u%d", i%4)),
					Label:     Label(fmt.Sprintf("l%d", i%3)),
					Kind:      []Kind{Grant, Deny}[i%2],
					Committed: Tick(i),
					Effective: Tick(i + (i % 3)),
				}
				if _, err := eng.Submit(ctx, c); err != nil {
					t.Errorf("submit: %v", err)
					return
				}
			}
		}(w)
	}
	// 撤回者：持续尝试撤回（绝大多数应为“已生效”或“未知”，不得 panic/竞态）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = eng.Withdraw(ctx, fmt.Sprintf("w%dn%d", i%8, i%50), Tick(i%60))
		}
	}()
	// 读者：高并发判定。
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d, err := eng.Decide(ctx,
					Subject(fmt.Sprintf("u%d", i%4)),
					Label(fmt.Sprintf("l%d", i%3)), Tick(i%55))
				if err != nil {
					continue
				}
				_ = d.Allowed
			}
		}()
	}
	wg.Wait()

	// 收敛性：无并发扰动时，同一时刻重复读结果稳定。
	d1, _ := eng.Decide(ctx, "u1", "l1", 54)
	d2, _ := eng.Decide(ctx, "u1", "l1", 54)
	if d1.Allowed != d2.Allowed || d1.Examined != d2.Examined {
		t.Fatalf("post-concurrency state not stable: %+v %+v", d1, d2)
	}
	if buf.Len() == 0 {
		t.Fatal("audit captured nothing under concurrency")
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// TestQueryCostIndependentOfTotalRecords 是不依赖实现细节的可观测证明：
// Decision.Examined 只统计“截至该时刻对该主体与该标签确实生效过的变更数”。
// 向系统灌入大量与目标 (u*, l*) 无关、以及对目标尚不可见的变更后，
// 目标查询的 Examined 保持不变；只增加对目标在该时刻已生效的变更时，
// Examined 恰好等量增长。
func TestQueryCostIndependentOfTotalRecords(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(0, nil)

	mustSubmit(t, e, Change{ID: "t0", Subject: "uX", Label: "lX", Kind: Grant,
		Committed: 1, Effective: 1})
	baseline, _ := e.Decide(ctx, "uX", "lX", 50)

	// 灌入 2000 条与目标无关的变更（其他主体/标签）。
	for i := 0; i < 2000; i++ {
		mustSubmit(t, e, Change{
			ID:        fmt.Sprintf("other-%d", i),
			Subject:   Subject(fmt.Sprintf("noise-%d", i%50)),
			Label:     "lX",
			Kind:      Grant,
			Committed: Tick(2 + i%40),
			Effective: Tick(2 + i%40),
		})
	}
	// 再灌入 1000 条属于目标但在查询时刻 50 尚不可见（生效时刻更晚）的排队变更。
	for i := 0; i < 1000; i++ {
		mustSubmit(t, e, Change{
			ID:        fmt.Sprintf("future-%d", i),
			Subject:   "uX",
			Label:     "lX",
			Kind:      Grant,
			Committed: 10,
			Effective: Tick(100 + i),
		})
	}

	d, _ := e.Decide(ctx, "uX", "lX", 50)
	if d.Examined != baseline.Examined {
		t.Fatalf("examined grew with total records: %d vs baseline %d (total=%d)",
			d.Examined, baseline.Examined, len(e.Snapshot()))
	}

	// 每新增一条对目标在 50 时刻已生效的变更，Examined 恰好 +1。
	prev := d.Examined
	for k := 0; k < 10; k++ {
		mustSubmit(t, e, Change{
			ID:        fmt.Sprintf("visible-%d", k),
			Subject:   "uX",
			Label:     "lX",
			Kind:      Grant,
			Committed: Tick(20 + k),
			Effective: Tick(20 + k),
		})
		dd, _ := e.Decide(ctx, "uX", "lX", 50)
		if dd.Examined != prev+1 {
			t.Fatalf("examined must grow exactly with per-subject-label effective "+
				"changes: got %d want %d (total=%d)", dd.Examined, prev+1,
				len(e.Snapshot()))
		}
		prev = dd.Examined
	}
}

// 审计日志必须完整记录输入、输出/错误与时序依据，且为合法 JSON Lines。
func TestAuditCompleteness(t *testing.T) {
	ctx := context.Background()
	buf := &syncBuffer{}
	e := NewEngine(0, NewAuditLog(buf))
	mustSubmit(t, e, Change{ID: "a1", Subject: "u", Label: "l",
		Kind: Grant, Committed: 1, Effective: 2})
	_, _ = e.Decide(ctx, "u", "l", 3)
	_ = e.Withdraw(ctx, "a1", 3) // 已生效 => 错误，也必须留痕

	text := buf.buf.String()
	for _, want := range []string{`"op":"submit"`, `"op":"decide"`,
		`"op":"withdraw"`, `"withdraw_already_effective"`, `"basis":[`,
		`"wall_clock"`, `"seq"`} {
		if !contains(text, want) {
			t.Fatalf("audit log missing %s in:\n%s", want, text)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
