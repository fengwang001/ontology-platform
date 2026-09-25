package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func eq64(a, b []int64) bool {
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
func span(lo, hi int64) []int64 { // 未确认集合恒等于有序区间 [lo,hi)
	s := []int64{}
	for i := lo; i < hi; i++ {
		s = append(s, i)
	}
	return s
}

// TestNaiveReference 钉不变量1（兼2）：随机交错后 Base 等于朴素参照（从0逐段、遇首个未覆盖段停下；ACK 到达时钳到当时 next）。
func TestNaiveReference(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		r := rand.New(rand.NewSource(int64(iter)))
		s, _ := api.New(5)
		var next, maxAck int64
		for op := 0; op < 60; op++ {
			switch r.Intn(3) {
			case 0:
				if s.Next()-s.Base() < 5 {
					seq, e := s.Send()
					if e != nil || int64(seq) != next {
						t.Fatalf("i%d send %d!=%d %v", iter, seq, next, e)
					}
					next++
				}
			case 1:
				a := r.Int63n(next + 3) // 含 0/重复/乱序/越过 next
				_ = s.Ack(a)
				if v := min(a, next); v > maxAck {
					maxAck = v
				}
			case 2:
				if g := s.Timeout(); !eq64(g, s.Unacked()) {
					t.Fatalf("i%d timeout %v!=%v", iter, g, s.Unacked())
				}
			}
			var nb int64 // 朴素：从0逐段，遇第一个 >=maxAck 的未覆盖段停下
			for nb < next && nb < maxAck {
				nb++
			}
			if s.Base() != nb || s.Next() != next || !eq64(s.Unacked(), span(nb, next)) || s.Next()-s.Base() > 5 {
				t.Fatalf("i%d op%d b=%d/%d n=%d/%d u=%v", iter, op, s.Base(), nb, s.Next(), next, s.Unacked())
			}
		}
	}
}
func TestTenStepTable(t *testing.T) {
	s, _ := api.New(4)
	want := [][2]int64{{0, 1}, {0, 2}, {0, 3}, {0, 4}, {2, 4}, {2, 5}, {2, 6}, {2, 6}, {5, 6}, {5, 6}}
	ops := []func(){
		func() { _, _ = s.Send() }, func() { _, _ = s.Send() }, func() { _, _ = s.Send() }, func() { _, _ = s.Send() },
		func() { _ = s.Ack(2) }, func() { _, _ = s.Send() }, func() { _, _ = s.Send() },
		func() { _ = s.Ack(1) }, func() { _ = s.Ack(5) },
	}
	for i, f := range ops {
		f()
		if s.Base() != want[i][0] || s.Next() != want[i][1] || !eq64(s.Unacked(), span(want[i][0], want[i][1])) {
			t.Fatalf("step%d b=%d/%d n=%d/%d u=%v", i+1, s.Base(), want[i][0], s.Next(), want[i][1], s.Unacked())
		}
	}
	if tmo := s.Timeout(); !eq64(tmo, []int64{5}) || s.Base() != want[9][0] || s.Next() != want[9][1] {
		t.Fatalf("step10 tmo=%v b=%d n=%d", tmo, s.Base(), s.Next())
	}
}

// TestQuestionCTwoOrders 钉(丙)：终 base 同为 3，重传集合随时序不同。
func TestQuestionCTwoOrders(t *testing.T) {
	build := func() *api.Sender {
		s, _ := api.New(4)
		for range 4 {
			_, _ = s.Send()
		}
		return s
	}
	a, b := build(), build()
	r1 := a.Timeout()
	_ = a.Ack(3)
	_ = b.Ack(3)
	r2 := b.Timeout()
	if !eq64(r1, []int64{0, 1, 2, 3}) || !eq64(r2, []int64{3}) || a.Base() != 3 || a.Base() != b.Base() || !eq64(a.Unacked(), b.Unacked()) {
		t.Fatalf("(丙) %v %v %d/%d", r1, r2, a.Base(), b.Base())
	}
}

// TestRejectedOpsLeaveNoTrace 钉不变量4：三类拒绝可判定、互异、不改状态，之后仍可用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, e := api.New(0); !errors.Is(e, api.ErrBadWindow) {
		t.Fatalf("New(0)=%v", e)
	}
	if api.ErrBadWindow == api.ErrBadAck || api.ErrBadAck == api.ErrWindowFull || api.ErrBadWindow == api.ErrWindowFull {
		t.Fatal("三类哨兵必须互异")
	}
	s, _ := api.New(4)
	for range 4 {
		_, _ = s.Send()
	}
	snap := func() [3]int64 { return [3]int64{s.Base(), s.Next(), int64(len(s.Unacked()))} }
	before := snap()
	if e := s.Ack(-1); !errors.Is(e, api.ErrBadAck) || snap() != before {
		t.Fatalf("负ACK %v %v", e, snap())
	}
	if _, e := s.Send(); !errors.Is(e, api.ErrWindowFull) || snap() != before {
		t.Fatalf("满窗Send %v", e)
	}
	_ = s.Ack(2) // 拒绝后仍可正常使用
	if seq, e := s.Send(); e != nil || seq != 4 || !eq64(s.Unacked(), []int64{2, 3, 4}) || !s.SelfCheck() {
		t.Fatalf("拒绝后失效/SelfCheck %d %v %v", seq, e, s.Unacked())
	}
}

// TestConcurrentReadOnly 钉第六节：N goroutine 并发只读喂满实例，逐字段相同；无 sleep。
func TestConcurrentReadOnly(t *testing.T) {
	s, _ := api.New(16)
	for range 16 {
		_, _ = s.Send()
	}
	var wg sync.WaitGroup
	got := make([][3]any, 64) // base, next, unacked 来自同一串行化时刻
	wg.Add(64)
	for i := range 64 {
		go func(i int) { defer wg.Done(); got[i] = [3]any{s.Base(), s.Next(), s.Unacked()} }(i)
	}
	wg.Wait()
	for i := 1; i < 64; i++ {
		if got[i][0] != got[0][0] || got[i][1] != got[0][1] || !eq64(got[i][2].([]int64), got[0][2].([]int64)) {
			t.Fatalf("g%d=%+v 与 g0=%+v 不一致", i, got[i], got[0])
		}
	}
}
