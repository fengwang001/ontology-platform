// Package api 是对外面孔：并发安全的会话封装与自检。依赖 flow。
package api

import (
	"fmt"
	"sync"

	"ontology/flow"
)

// 对外再导出三类哨兵错误，调用方可用 errors.Is 判定。
var (
	ErrWindowExceeded = flow.ErrWindowExceeded
	ErrBadAck         = flow.ErrBadAck
	ErrBadWindow      = flow.ErrBadWindow
)

// Session 是一条可并发访问的发送会话。
type Session struct {
	mu sync.RWMutex
	s  *flow.Sender
}

// New 以初始通告窗口 w0 创建会话。
func New(w0 int64) *Session { return &Session{s: flow.NewSender(w0)} }

func (s *Session) Send(n int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s.Send(n)
}

func (s *Session) RecvAck(a int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s.RecvAck(a)
}

func (s *Session) RecvWindow(w int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s.RecvWindow(w)
}

func (s *Session) Una() int64   { s.mu.RLock(); defer s.mu.RUnlock(); return s.s.Una() }
func (s *Session) Next() int64  { s.mu.RLock(); defer s.mu.RUnlock(); return s.s.Next() }
func (s *Session) Wnd() int64   { s.mu.RLock(); defer s.mu.RUnlock(); return s.s.Wnd() }
func (s *Session) Right() int64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.s.Right() }
func (s *Session) Avail() int64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.s.Avail() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 只在内部新建实例上运行，不触碰接收者状态，可并发调用。
func (s *Session) SelfCheck() error {
	// 不变量 1/2/3：八步序列的逐步状态必须等于手推表。
	type row struct{ una, next, wnd, right, avail int64 }
	steps := []struct {
		op   func(*Session) error
		want row
		err  error
	}{
		{func(x *Session) error { return x.Send(60) }, row{0, 60, 100, 100, 40}, nil},
		{func(x *Session) error { return x.RecvWindow(50) }, row{0, 60, 100, 100, 40}, nil},
		{func(x *Session) error { return x.Send(40) }, row{0, 100, 100, 100, 0}, nil},
		{func(x *Session) error { return x.RecvAck(100) }, row{100, 100, 100, 200, 100}, nil},
		{func(x *Session) error { return x.RecvWindow(0) }, row{100, 100, 0, 100, 0}, nil},
		{func(x *Session) error { return x.Send(50) }, row{100, 100, 0, 100, 0}, ErrWindowExceeded},
		{func(x *Session) error { return x.RecvWindow(80) }, row{100, 100, 80, 180, 80}, nil},
		{func(x *Session) error { return x.Send(80) }, row{100, 180, 80, 180, 0}, nil},
	}
	tr := New(100)
	prevRight := tr.Right()
	for i, st := range steps {
		err := st.op(tr)
		if err != st.err {
			return fmt.Errorf("selfcheck step %d: err=%v want %v", i+1, err, st.err)
		}
		got := row{tr.Una(), tr.Next(), tr.Wnd(), tr.Right(), tr.Avail()}
		if got != st.want {
			return fmt.Errorf("selfcheck step %d: got %+v want %+v", i+1, got, st.want)
		}
		if tr.Avail() < 0 { // 不变量 3
			return fmt.Errorf("selfcheck step %d: negative avail", i+1)
		}
		if tr.Right() < prevRight && !(tr.Wnd() == 0 && tr.Right() == tr.Una()) {
			return fmt.Errorf("selfcheck step %d: right shrank without zero window", i+1)
		}
		prevRight = tr.Right()
	}
	// 不变量 4：三类被拒操作均不留痕，且之后可正常使用。
	rj := New(10)
	before := [5]int64{rj.Una(), rj.Next(), rj.Wnd(), rj.Right(), rj.Avail()}
	for _, op := range []func(*Session) error{
		func(x *Session) error { return x.Send(11) },   // ErrWindowExceeded
		func(x *Session) error { return x.RecvAck(1) }, // ErrBadAck（a>next）
		func(x *Session) error { return x.RecvWindow(-1) },
	} {
		if op(rj) == nil {
			return fmt.Errorf("selfcheck: rejected op returned nil")
		}
	}
	after := [5]int64{rj.Una(), rj.Next(), rj.Wnd(), rj.Right(), rj.Avail()}
	if before != after {
		return fmt.Errorf("selfcheck: rejected ops changed state %v -> %v", before, after)
	}
	if err := rj.Send(10); err != nil {
		return fmt.Errorf("selfcheck: session unusable after rejections: %v", err)
	}
	return nil
}
