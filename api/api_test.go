package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// naive 是题目规则的朴素参照实现，逐步手推语义。
type naive struct{ una, next, wnd int64 }

func (n *naive) right() int64 { return n.una + n.wnd }
func (n *naive) avail() int64 { return max(0, n.right()-n.next) }
func (n *naive) send(x int64) error {
	if x <= 0 || x > n.avail() {
		return api.ErrWindowExceeded
	}
	n.next += x
	return nil
}
func (n *naive) ack(a int64) error {
	if a < n.una || a > n.next {
		return api.ErrBadAck
	}
	n.una = a
	return nil
}
func (n *naive) window(w int64) error {
	if w < 0 {
		return api.ErrBadWindow
	}
	if w == 0 || n.una+w >= n.right() {
		n.wnd = w
	}
	return nil
}
func stateOf(s *api.Sender) [5]int64 {
	return [5]int64{s.Una(), s.Next(), s.Wnd(), s.Right(), s.Avail()}
}

// walk 对 seed ∈ [lo,hi) 的随机操作序列逐步核验不变量 1/2/3。
func walk(t *testing.T, lo, hi int64, ops int) {
	for seed := lo; seed < hi; seed++ {
		r := rand.New(rand.NewSource(seed))
		s, n := api.New(100), &naive{wnd: 100}
		prev := s.Right()
		for i := 0; i < ops; i++ {
			var gErr, nErr error
			zero := false
			switch r.Intn(3) {
			case 0:
				x, av := int64(r.Intn(120)), s.Avail()
				gErr, nErr = s.Send(x), n.send(x)
				if (gErr == nil) != (x > 0 && x <= av) { // 不变量 3：恰在 avail 内放行
					t.Fatalf("seed=%d op=%d: Send(%d) err=%v avail=%d", seed, i, x, gErr, av)
				}
			case 1:
				a := s.Una() + int64(r.Intn(120)) - 10 // 覆盖越界确认
				gErr, nErr = s.RecvAck(a), n.ack(a)
			case 2:
				w := int64(r.Intn(160)) - 5 // 覆盖负窗口与零窗口
				zero = w == 0
				gErr, nErr = s.RecvWindow(w), n.window(w)
			}
			want := [5]int64{n.una, n.next, n.wnd, n.right(), n.avail()}
			if !errors.Is(gErr, nErr) || stateOf(s) != want { // 不变量 1（含 avail>=0）
				t.Fatalf("seed=%d op=%d: err=%v/%v state=%v want %v", seed, i, gErr, nErr, stateOf(s), want)
			}
			if got := s.Right(); zero && got != s.Una() { // 不变量 2：零窗口收缩到 una
				t.Fatalf("seed=%d op=%d: zero right=%d una=%d", seed, i, got, s.Una())
			} else if !zero && got < prev {
				t.Fatalf("seed=%d op=%d: right shrank %d -> %d", seed, i, prev, got)
			}
			prev = s.Right()
		}
	}
}

func TestMatchesNaive(t *testing.T) { walk(t, 0, 50, 200) }

func TestRightEdgeMonotonic(t *testing.T) { walk(t, 100, 130, 150) }

func TestSendWithinWindow(t *testing.T) { walk(t, 200, 230, 150) }

// TestRejectedOpsKeepState 不变量 4：三类错误互不相同、被拒后状态不变、之后仍可用。
func TestRejectedOpsKeepState(t *testing.T) {
	if errors.Is(api.ErrWindowExceeded, api.ErrBadAck) || errors.Is(api.ErrBadAck, api.ErrBadWindow) ||
		errors.Is(api.ErrBadWindow, api.ErrWindowExceeded) {
		t.Fatal("sentinel errors must be distinct")
	}
	cases := []struct {
		name string
		wnd  int64 // 先通告的窗口（10=不变，0=零窗口）
		op   func(*api.Sender) error
		err  error
	}{
		{"window-exceeded", 10, func(s *api.Sender) error { return s.Send(11) }, api.ErrWindowExceeded},
		{"zero-window-send", 0, func(s *api.Sender) error { return s.Send(1) }, api.ErrWindowExceeded},
		{"ack-too-low", 10, func(s *api.Sender) error { return s.RecvAck(-1) }, api.ErrBadAck},
		{"ack-too-high", 10, func(s *api.Sender) error { return s.RecvAck(6) }, api.ErrBadAck},
		{"negative-window", 10, func(s *api.Sender) error { return s.RecvWindow(-1) }, api.ErrBadWindow},
	}
	for _, c := range cases {
		s := api.New(10)
		_ = s.Send(5)
		_ = s.RecvWindow(c.wnd)
		before := stateOf(s)
		if err := c.op(s); !errors.Is(err, c.err) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.err)
		}
		if after := stateOf(s); after != before {
			t.Fatalf("%s: state changed %v -> %v", c.name, before, after)
		}
		if err := s.RecvAck(5); err != nil { // 拒绝后仍可正常使用
			t.Fatalf("%s: unusable after rejection: %v", c.name, err)
		}
	}
}

// TestConcurrentReaders 并发只读：N 个 goroutine 逐字段相同，无 sleep。
func TestConcurrentReaders(t *testing.T) {
	s := api.New(1000)
	_ = s.Send(400)
	want := stateOf(s)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if got := stateOf(s); got != want {
				bad.Add(1)
			}
			if err := s.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("concurrent readers disagree")
	}
}
