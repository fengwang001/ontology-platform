package buf

import (
	"fmt"
	"testing"
)

// TestTakeTickTriggers 表驱动核验两档触发、批量优先、age==L 边界与取出 FIFO 顺序。
func TestTakeTickTriggers(t *testing.T) {
	cases := []struct {
		n, b, l int
		now     int64
		kind    Kind
		size    int
	}{
		{5, 3, 2, 0, BySize, 3},    // len>=B：批量触发
		{4, 3, 2, 5, BySize, 3},    // 两档同时成立仍判批量，只取头部 B
		{3, 3, 2, 1, BySize, 3},    // age 未到但 len==B
		{2, 3, 2, 2, ByLatency, 2}, // len<B 且最旧 age==L（等号边界）
		{2, 3, 2, 1, None, 0},      // age=1<L：不触发
		{0, 3, 2, 9, None, 0},      // 空缓冲不触发
	}
	for _, c := range cases {
		q := New(c.b, 100, c.l)
		for i := 0; i < c.n; i++ {
			q.Push(fmt.Sprintf("k%d", i), i, 0)
		}
		batch, _, kind := q.TakeTick(c.now)
		if kind != c.kind || len(batch) != c.size || q.Len() != c.n-c.size {
			t.Fatalf("n=%d now=%d: kind=%d size=%d rem=%d, want %d/%d/%d",
				c.n, c.now, kind, len(batch), q.Len(), c.kind, c.size, c.n-c.size)
		}
		for i, en := range batch {
			if en.Key != fmt.Sprintf("k%d", i) {
				t.Fatalf("FIFO 顺序被破坏: %v", batch)
			}
		}
	}
}

// TestHighWater 表驱动核验 len>=H 即满（含 len==H 边界）。
func TestHighWater(t *testing.T) {
	cases := []struct {
		b, h, pushes int
		full         bool
	}{
		{3, 4, 3, false}, {3, 4, 4, true}, {1, 1, 0, false}, {1, 1, 1, true},
	}
	for _, c := range cases {
		q := New(c.b, c.h, 1)
		for i := 0; i < c.pushes; i++ {
			q.Push("k", i, 0)
		}
		if q.Full() != c.full || q.Len() != c.pushes {
			t.Fatalf("%+v: full=%v len=%d", c, q.Full(), q.Len())
		}
	}
}

// TestReturnRollback 核验失败批按原顺序整体放回缓冲头部。
func TestReturnRollback(t *testing.T) {
	q := New(3, 10, 2)
	for i := 0; i < 5; i++ {
		q.Push(fmt.Sprintf("k%d", i), i, 0)
	}
	batch, lease, kind := q.TakeTick(0) // len=5>=B，取出头部 3
	if kind != BySize || len(batch) != 3 || q.Len() != 2 {
		t.Fatalf("take: kind=%d size=%d rem=%d", kind, len(batch), q.Len())
	}
	q.Return(lease)
	if q.Len() != 5 {
		t.Fatalf("rollback 后长度=%d, want 5", q.Len())
	}
	var got []string
	for q.Len() > 0 {
		bb, _, _ := q.TakeFlush()
		for _, en := range bb {
			got = append(got, en.Key)
		}
	}
	for i := 0; i < 5; i++ {
		if got[i] != fmt.Sprintf("k%d", i) {
			t.Fatalf("回滚后 FIFO 顺序错乱: %v", got)
		}
	}
}

// TestTakeFlushSized 核验强制 flush 每批至多 B 条、空缓冲不发火。
func TestTakeFlushSized(t *testing.T) {
	q := New(3, 10, 100)
	if _, _, fired := q.TakeFlush(); fired || q.scan != 0 {
		t.Fatal("空缓冲不应取出批次")
	}
	for i := 0; i < 5; i++ {
		q.Push(fmt.Sprintf("k%d", i), i, 0)
	}
	first, _, f1 := q.TakeFlush()
	second, _, f2 := q.TakeFlush()
	if !f1 || !f2 || len(first) != 3 || len(second) != 2 || q.Len() != 0 {
		t.Fatalf("强制 flush 分批错误: %d/%d", len(first), len(second))
	}
}

// TestTakeComplexity 钉住复杂度：灌满 m 条（不 Tick），强制 flush 一批，
// 非导出计数器 scan 必须 ≤ B+1，不随 m 线性增长（多档 m 用循环生成）。
func TestTakeComplexity(t *testing.T) {
	const B = 3
	for _, m := range []int{100, 1000, 10000} {
		q := New(B, m+1, 1<<30)
		for i := 0; i < m; i++ {
			q.Push(fmt.Sprintf("k%d", i), i, 0)
		}
		batch, _, fired := q.TakeFlush()
		if !fired || len(batch) != B || q.Len() != m-B {
			t.Fatalf("m=%d: size=%d rem=%d", m, len(batch), q.Len())
		}
		if batch[0].Key != "k0" || batch[B-1].Key != "k2" {
			t.Fatalf("m=%d: 取出的不是头部", m)
		}
		if q.scan > B+1 {
			t.Fatalf("m=%d: scan=%d 随总长度增长，非按头取批（应≤%d）", m, q.scan, B+1)
		}
	}
}

// TestBatchBound 不变量2：TakeTick/TakeFlush 取出的每批条目数都在 [1,B]（多档 B）。
func TestBatchBound(t *testing.T) {
	for _, B := range []int{1, 3, 5} {
		q := New(B, 1000, 2)
		for i := 0; i < 50; i++ {
			q.Push(fmt.Sprintf("k%d", i), i, int64(i%3))
		}
		for q.Len() > 0 {
			batch, _, kind := q.TakeTick(1000) // 大 now：非空必触发批量或延迟
			if kind == None || len(batch) < 1 || len(batch) > B {
				t.Fatalf("B=%d 非法批次 kind=%d size=%d", B, kind, len(batch))
			}
		}
	}
}
