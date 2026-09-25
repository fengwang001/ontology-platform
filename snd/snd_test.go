package snd

import (
	"errors"
	"testing"
)

func eqInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewRejectsBadWindow(t *testing.T) {
	for _, W := range []int{-5, -1, 0} {
		w, err := New(W)
		if !errors.Is(err, ErrBadWindow) || w != nil {
			t.Fatalf("New(%d): want ErrBadWindow,nil got %v,%v", W, w, err)
		}
	}
}

// TestWindowBound 钉住不变量2：next-base<=W，满窗 Send 被 ErrWindowFull 拒且不留痕。
func TestWindowBound(t *testing.T) {
	cases := []struct {
		W    int
		send int
	}{{1, 1}, {2, 2}, {4, 4}, {7, 7}, {100, 100}}
	for _, c := range cases {
		w, _ := New(c.W)
		for range c.send {
			if _, err := w.Send(); err != nil {
				t.Fatalf("W=%d 未预期发送失败: %v", c.W, err)
			}
		}
		if w.next-w.base != int64(c.W) || w.next-w.base > int64(w.w) {
			t.Fatalf("W=%d 窗口占满后越界 base=%d next=%d", c.W, w.base, w.next)
		}
		if _, err := w.Send(); !errors.Is(err, ErrWindowFull) {
			t.Fatalf("W=%d 满窗 Send 想要 ErrWindowFull, 得 %v", c.W, err)
		}
		if w.next != int64(c.send) || w.base != 0 { // 被拒不改指针
			t.Fatalf("W=%d 满窗被拒后状态变化 base=%d next=%d", c.W, w.base, w.next)
		}
	}
}

// TestAckMonotonicIdempotent 钉住不变量3：base 只进不退，a<=base 完全 no-op。
func TestAckMonotonicIdempotent(t *testing.T) {
	w, _ := New(4)
	for range 4 {
		_, _ = w.Send()
	}
	w.Advance(2)
	if w.base != 2 || w.check != 0 || !eqInt64(w.Unacked(), []int64{2, 3}) {
		t.Fatalf("Ack(2) 后状态错误 base=%d unacked=%v", w.base, w.Unacked())
	}
	for _, a := range []int64{0, 1, 2} { // 重复/乱序/边界
		before := w.check
		w.Advance(a)
		if w.base != 2 || w.check != before || !eqInt64(w.Unacked(), []int64{2, 3}) {
			t.Fatalf("重复 ACK %d 改变了状态 base=%d check=%d", a, w.base, w.check)
		}
	}
	w.Advance(99) // ACK 超过 next 被钳到 next=4
	if w.base != 4 || w.base > w.next {
		t.Fatalf("ACK 越界未钳制 base=%d next=%d", w.base, w.next)
	}
}

func TestTimeoutGoBackN(t *testing.T) {
	w, _ := New(4)
	for range 4 {
		_, _ = w.Send()
	}
	if got := w.Timeout(); !eqInt64(got, []int64{0, 1, 2, 3}) {
		t.Fatalf("满窗超时应重传 [0,4), 得 %v", got)
	}
	if w.base != 0 || w.next != 4 { // 超时不改指针
		t.Fatalf("Timeout 改了 base/next: %d,%d", w.base, w.next)
	}
	w.Advance(3)
	if got := w.Timeout(); !eqInt64(got, []int64{3}) {
		t.Fatalf("推进后超时只应重传 {3}, 得 %v", got)
	}
	e, _ := New(2)
	if got := e.Timeout(); got != nil { // 无未确认段
		t.Fatalf("空窗口超时应返回 nil, 得 %v", got)
	}
}

// TestAdvanceChecksConstant 钉住第四节：一次 Ack(m) 推进到底，检查段数恒为 0、
// 不随 m（100..10000）线性增长。计数器为非导出字段，仅在此白盒测试直读。
func TestAdvanceChecksConstant(t *testing.T) {
	ms := []int{100, 1000, 10000}
	var seen []int
	for _, m := range ms {
		w, _ := New(m)
		for range m {
			_, _ = w.Send()
		}
		if w.base != 0 || w.next != int64(m) {
			t.Fatalf("m=%d 准备态错误 base=%d next=%d", m, w.base, w.next)
		}
		w.Advance(int64(m))
		if w.Base() != int64(m) {
			t.Fatalf("m=%d 推进后 base=%d", m, w.Base())
		}
		if w.check != 0 {
			t.Fatalf("m=%d 推进检查段数=%d，应为与 m 无关的常数 0", m, w.check)
		}
		seen = append(seen, w.check)
	}
	for i := 1; i < len(seen); i++ { // 不随 m 增长
		if seen[i] != seen[0] {
			t.Fatalf("检查计数随 m 增长: %v", seen)
		}
	}
}
