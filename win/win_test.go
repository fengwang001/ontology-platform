package win

import "testing"

// TestCounterConstant 证明 inflight/right 用计数器与边界指针 O(1) 维护：
// 先制造 m 字节在途，再一次 RecvAck(m) 确认到底，最近一次操作检查过的
// 在途单元个数不得超过与 m 无关的小常数。白盒读取非导出字段 checked。
func TestCounterConstant(t *testing.T) {
	const limit = 2 // 与 m 无关的小常数：每次操作只查边界指针
	for _, m := range []int64{100, 1000, 10000} {
		w := New(m)
		for i := int64(0); i < m; i++ {
			if !w.CanSend(1) {
				t.Fatalf("m=%d: send %d rejected", m, i)
			}
			w.ApplySend(1)
		}
		if got := w.checked; got > limit {
			t.Fatalf("m=%d: Send checked %d units > %d", m, got, limit)
		}
		if !w.ValidAck(m) {
			t.Fatalf("m=%d: ack %d rejected", m, m)
		}
		w.ApplyAck(m)
		if got := w.checked; got > limit {
			t.Fatalf("m=%d: RecvAck checked %d units > %d", m, got, limit)
		}
		w.ApplyWindow(m)
		if got := w.checked; got > limit {
			t.Fatalf("m=%d: RecvWindow checked %d units > %d", m, got, limit)
		}
		if w.Una() != m || w.Next() != m || w.Avail() != m {
			t.Fatalf("m=%d: bad state una=%d next=%d avail=%d", m, w.Una(), w.Next(), w.Avail())
		}
	}
}

// TestShrinkAndZero 钉住 win 层的收缩判定：非零收缩忽略、零窗口接受。
func TestShrinkAndZero(t *testing.T) {
	cases := []struct {
		name               string
		w0, adv            int64
		wantWnd, wantRight int64
	}{
		{"expand", 100, 150, 150, 150},
		{"equal", 100, 100, 100, 100},
		{"shrink-ignored", 100, 50, 100, 100},
		{"zero-accepted", 100, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := New(c.w0)
			w.ApplyWindow(c.adv)
			if w.Wnd() != c.wantWnd || w.Right() != c.wantRight {
				t.Fatalf("wnd=%d right=%d want %d/%d", w.Wnd(), w.Right(), c.wantWnd, c.wantRight)
			}
		})
	}
}
