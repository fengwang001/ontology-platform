package api_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/api"
)

type snap struct{ una, next, wnd, right, avail int64 }
type step struct {
	op, v int64 // op: 0=Send 1=RecvAck 2=RecvWindow
	want  snap
	err   error
}

func take(s *api.Session) snap { return snap{s.Una(), s.Next(), s.Wnd(), s.Right(), s.Avail()} }
func apply(s *api.Session, op, v int64) error {
	if op == 0 {
		return s.Send(v)
	}
	if op == 1 {
		return s.RecvAck(v)
	}
	return s.RecvWindow(v)
}

// runSeq 逐步执行序列：错误符合预期、状态等于 want（零值跳过）、右边缘单调（零窗口例外）。
func runSeq(t *testing.T, s *api.Session, seq []step) {
	prev := s.Right()
	for i, st := range seq {
		if err := apply(s, st.op, st.v); !errors.Is(err, st.err) {
			t.Fatalf("op %d: err=%v want %v", i+1, err, st.err)
		}
		if st.want != (snap{}) && take(s) != st.want {
			t.Fatalf("op %d: got %+v want %+v", i+1, take(s), st.want)
		}
		if r := s.Right(); r < prev && !(s.Wnd() == 0 && r == s.Una()) {
			t.Fatalf("op %d: right %d -> %d without zero window", i+1, prev, r)
		}
		prev = s.Right()
	}
}
func TestEightStepTrace(t *testing.T) {
	runSeq(t, api.New(100), []step{
		{0, 60, snap{0, 60, 100, 100, 40}, nil},
		{2, 50, snap{0, 60, 100, 100, 40}, nil},
		{0, 40, snap{0, 100, 100, 100, 0}, nil},
		{1, 100, snap{100, 100, 100, 200, 100}, nil},
		{2, 0, snap{100, 100, 0, 100, 0}, nil},
		{0, 50, snap{100, 100, 0, 100, 0}, api.ErrWindowExceeded},
		{2, 80, snap{100, 100, 80, 180, 80}, nil},
		{0, 80, snap{100, 180, 80, 180, 0}, nil},
	})
}
func TestRightEdgeMonotone(t *testing.T) {
	runSeq(t, api.New(100), []step{
		{op: 0, v: 60}, {op: 2, v: 50}, {op: 0, v: 40}, {op: 1, v: 100},
		{op: 2, v: 0}, {op: 2, v: 30}, {op: 2, v: 80}, {op: 0, v: 80},
	})
}

type model struct{ una, next, wnd, right int64 } // 测试内的朴素参照实现，按规则逐步手推

func (m *model) apply(op, v int64) error {
	switch {
	case op == 0 && (v <= 0 || v > max(m.right-m.next, 0)):
		return api.ErrWindowExceeded
	case op == 0:
		m.next += v
	case op == 1 && (v < m.una || v > m.next):
		return api.ErrBadAck
	case op == 1:
		m.una, m.right = v, v+m.wnd
	case v < 0:
		return api.ErrBadWindow
	case v == 0:
		m.wnd, m.right = 0, m.una
	default:
		if c := m.una + v; c >= m.right {
			m.wnd, m.right = v, c
		}
	}
	return nil
}
func TestRandomSequencesMatchModel(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		rng, s, m := rand.New(rand.NewSource(seed)), api.New(100), &model{wnd: 100, right: 100}
		for i := 0; i < 1500; i++ {
			op, v := int64(rng.Intn(3)), rng.Int63n(220)-10
			if op == 1 {
				v = m.una - 5 + rng.Int63n(m.next-m.una+11)
			}
			gerr, werr := apply(s, op, v), m.apply(op, v)
			want := snap{m.una, m.next, m.wnd, m.right, max(m.right-m.next, 0)}
			if !errors.Is(gerr, werr) || take(s) != want {
				t.Fatalf("seed=%d i=%d: err %v/%v state %v/%v", seed, i, gerr, werr, take(s), want)
			}
		}
	}
}
func TestSendBoundedAndAvailNonNegative(t *testing.T) {
	s, rng := api.New(50), rand.New(rand.NewSource(7))
	for i := 0; i < 300; i++ {
		op, v := int64(rng.Intn(3)), rng.Int63n(120)
		if op == 1 {
			v = s.Una() + rng.Int63n(s.Next()-s.Una()+1)
		}
		_ = apply(s, op, v)
		if s.Avail() < 0 {
			t.Fatalf("op %d: avail=%d < 0", i, s.Avail())
		}
		if err := s.Send(s.Avail() + 1); !errors.Is(err, api.ErrWindowExceeded) {
			t.Fatalf("op %d: send beyond avail err=%v", i, err)
		}
	}
}
func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	ok := snap{5, 10, 10, 15, 5} // Send(10)+RecvAck(5) 后的状态，拒绝操作不得改变它
	runSeq(t, api.New(10), []step{
		{0, 10, snap{0, 10, 10, 10, 0}, nil},
		{1, 5, ok, nil},
		{0, 6, ok, api.ErrWindowExceeded},
		{1, 4, ok, api.ErrBadAck},
		{1, 11, ok, api.ErrBadAck},
		{2, -1, ok, api.ErrBadWindow},
		{0, 5, snap{5, 15, 10, 15, 0}, nil}, // 拒绝后仍可正常使用
	})
}
func TestConcurrentReadOnly(t *testing.T) {
	s := api.New(64)
	if err := s.Send(30); err != nil {
		t.Fatal(err)
	}
	want, start, done := take(s), make(chan struct{}), make(chan snap, 32)
	for i := 0; i < 32; i++ {
		go func() {
			<-start
			done <- take(s)
			_ = s.SelfCheck()
		}()
	}
	close(start)
	for i := 0; i < 32; i++ {
		if got := <-done; got != want {
			t.Fatalf("reader %d: %v != %v", i, got, want)
		}
	}
}
