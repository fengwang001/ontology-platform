// Package flow 在 win 记账之上推进流控事件：
// Send/RecvAck/RecvWindow 的执行与错误判定。依赖 win。
package flow

import (
	"errors"

	"ontology/win"
)

// 三类可判定故障，互不相同的哨兵错误。
var (
	ErrWindowExceeded = errors.New("flow: send exceeds advertised window")
	ErrBadAck         = errors.New("flow: ack outside [una, next]")
	ErrBadWindow      = errors.New("flow: negative window advertisement")
)

// Sender 推进一条发送流。校验全部前置，失败不留痕。
type Sender struct {
	w *win.Window
}

// NewSender 以初始通告窗口 w0 创建发送流。
func NewSender(w0 int64) *Sender { return &Sender{w: win.New(w0)} }

// Send 发送 n 字节；越窗（含零窗口期）报 ErrWindowExceeded。
func (s *Sender) Send(n int64) error {
	if !s.w.Sendable(n) {
		return ErrWindowExceeded
	}
	s.w.AdvanceSend(n)
	return nil
}

// RecvAck 累积确认到 a；a 不在 [una, next] 报 ErrBadAck。
func (s *Sender) RecvAck(a int64) error {
	if !s.w.AckValid(a) {
		return ErrBadAck
	}
	s.w.ApplyAck(a)
	return nil
}

// RecvWindow 通告窗口 w；w 为负报 ErrBadWindow。
// 非零收缩在 win.ApplyWindow 内被忽略，零窗口被接受。
func (s *Sender) RecvWindow(w int64) error {
	if !s.w.WindowValid(w) {
		return ErrBadWindow
	}
	s.w.ApplyWindow(w)
	return nil
}

func (s *Sender) Una() int64   { return s.w.Una() }
func (s *Sender) Next() int64  { return s.w.Next() }
func (s *Sender) Wnd() int64   { return s.w.Wnd() }
func (s *Sender) Right() int64 { return s.w.Right() }
func (s *Sender) Avail() int64 { return s.w.Avail() }

// Snapshot 返回当前五个观测量。
func (s *Sender) Snapshot() (una, next, wnd, right, avail int64) {
	return s.w.Una(), s.w.Next(), s.w.Wnd(), s.w.Right(), s.w.Avail()
}
