// Package api 是对外接口层：创建发送方、推进流控事件、只读视图与自检。
// 依赖 flow，不反向依赖。
package api

import (
	"errors"
	"fmt"

	"ontology/flow"
)

// 对外暴露的三类哨兵错误，与 flow 中定义同一实例，可用 errors.Is 判定。
var (
	ErrWindowExceeded = flow.ErrWindowExceeded
	ErrBadAck         = flow.ErrBadAck
	ErrBadWindow      = flow.ErrBadWindow
)

// Sender 是可靠传输发送方的窗口状态机，并发安全。
type Sender struct {
	f *flow.Flow
}

// New 创建 Sender：una=next=0，wnd=w0，right=w0。
func New(w0 int64) *Sender { return &Sender{f: flow.New(w0)} }

// Send 尝试发送 n 字节，越窗返回 ErrWindowExceeded 且状态不变。
func (s *Sender) Send(n int64) error { return s.f.Send(n) }

// RecvAck 应用累积确认，非法返回 ErrBadAck 且状态不变。
func (s *Sender) RecvAck(a int64) error { return s.f.RecvAck(a) }

// RecvWindow 应用窗口通告，w 为负返回 ErrBadWindow 且状态不变。
func (s *Sender) RecvWindow(w int64) error { return s.f.RecvWindow(w) }

// Una 返回累计已确认字节。
func (s *Sender) Una() int64 { u, _, _, _, _ := s.f.Snapshot(); return u }

// Next 返回下一发送字节序号。
func (s *Sender) Next() int64 { _, n, _, _, _ := s.f.Snapshot(); return n }

// Wnd 返回最近通告的窗口大小。
func (s *Sender) Wnd() int64 { _, _, w, _, _ := s.f.Snapshot(); return w }

// Right 返回窗口右边缘 una+wnd。
func (s *Sender) Right() int64 { _, _, _, r, _ := s.f.Snapshot(); return r }

// Avail 返回可发送空间 max(0, right-next)。
func (s *Sender) Avail() int64 { _, _, _, _, a := s.f.Snapshot(); return a }

// SelfCheck 在内置操作序列上核验四条不变量，全部通过返回 nil。
func (s *Sender) SelfCheck() error {
	if err := checkEightSteps(); err != nil {
		return err
	}
	if err := checkRejectedKeepState(); err != nil {
		return err
	}
	return checkRightEdgeMonotonic()
}

// checkEightSteps 核验不变量 1、3：八步序列逐步等于手推结果。
func checkEightSteps() error {
	type want struct {
		una, next, wnd, right, avail int64
		err                          error
	}
	steps := []struct {
		op func(*Sender) error
		want
	}{
		{func(s *Sender) error { return s.Send(60) }, want{0, 60, 100, 100, 40, nil}},
		{func(s *Sender) error { return s.RecvWindow(50) }, want{0, 60, 100, 100, 40, nil}},
		{func(s *Sender) error { return s.Send(40) }, want{0, 100, 100, 100, 0, nil}},
		{func(s *Sender) error { return s.RecvAck(100) }, want{100, 100, 100, 200, 100, nil}},
		{func(s *Sender) error { return s.RecvWindow(0) }, want{100, 100, 0, 100, 0, nil}},
		{func(s *Sender) error { return s.Send(50) }, want{100, 100, 0, 100, 0, ErrWindowExceeded}},
		{func(s *Sender) error { return s.RecvWindow(80) }, want{100, 100, 80, 180, 80, nil}},
		{func(s *Sender) error { return s.Send(80) }, want{100, 180, 80, 180, 0, nil}},
	}
	s := New(100)
	for i, st := range steps {
		if err := st.op(s); !errors.Is(err, st.err) {
			return fmt.Errorf("selfcheck step %d: err=%v want %v", i+1, err, st.err)
		}
		g := want{s.Una(), s.Next(), s.Wnd(), s.Right(), s.Avail(), st.err}
		if g != st.want {
			return fmt.Errorf("selfcheck step %d: state=%+v want %+v", i+1, g, st.want)
		}
	}
	return nil
}

// checkRejectedKeepState 核验不变量 4：被拒操作不改变任何状态。
func checkRejectedKeepState() error {
	s := New(10)
	if err := s.Send(10); err != nil {
		return fmt.Errorf("selfcheck setup: %v", err)
	}
	before := [5]int64{s.Una(), s.Next(), s.Wnd(), s.Right(), s.Avail()}
	for _, op := range []func() error{
		func() error { return s.Send(1) },        // ErrWindowExceeded
		func() error { return s.RecvAck(11) },    // ErrBadAck
		func() error { return s.RecvWindow(-1) }, // ErrBadWindow
	} {
		if op() == nil {
			return errors.New("selfcheck: rejected op returned nil")
		}
		after := [5]int64{s.Una(), s.Next(), s.Wnd(), s.Right(), s.Avail()}
		if after != before {
			return fmt.Errorf("selfcheck: rejected op changed state %v -> %v", before, after)
		}
	}
	return nil
}

// checkRightEdgeMonotonic 核验不变量 2：右边缘只增不减，零窗口收缩到 una 是唯一例外。
func checkRightEdgeMonotonic() error {
	s := New(100)
	if err := s.Send(100); err != nil {
		return fmt.Errorf("selfcheck setup: %v", err)
	}
	if err := s.RecvAck(100); err != nil {
		return fmt.Errorf("selfcheck setup: %v", err)
	}
	prev := s.Right() // 200
	for _, w := range []int64{150, 0, 80} {
		if err := s.RecvWindow(w); err != nil {
			return fmt.Errorf("selfcheck window: %v", err)
		}
		r := s.Right()
		switch {
		case w == 0 && r != s.Una():
			return fmt.Errorf("selfcheck: zero window right=%d want una=%d", r, s.Una())
		case w > 0 && r < prev:
			return fmt.Errorf("selfcheck: right shrank %d -> %d", prev, r)
		}
		prev = r
	}
	return nil
}
