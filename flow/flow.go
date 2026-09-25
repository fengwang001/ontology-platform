// Package flow 在 win 的窗口记账之上执行流控事件推进：
// Send/RecvAck/RecvWindow 的校验与状态迁移，失败整体不留痕。
// 依赖 win，不依赖 api。
package flow

import (
	"errors"
	"sync"

	"ontology/win"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrWindowExceeded = errors.New("flow: send exceeds advertised window")
	ErrBadAck         = errors.New("flow: ack outside [una, next]")
	ErrBadWindow      = errors.New("flow: negative window advertisement")
)

// Flow 是带并发保护的流控状态机。所有读写都经 mu，只读访问可并发。
type Flow struct {
	mu sync.RWMutex
	w  win.Window
}

// New 返回初始流控状态：una=next=0，wnd=w0。
func New(w0 int64) *Flow { return &Flow{w: win.New(w0)} }

// Send 尝试发送 n 字节；越窗（含零窗口期）整体失败，状态不变。
func (f *Flow) Send(n int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.w.CanSend(n) {
		return ErrWindowExceeded
	}
	f.w.ApplySend(n)
	return nil
}

// RecvAck 应用累积确认 a；a 不在 [una, next] 内整体失败，状态不变。
func (f *Flow) RecvAck(a int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.w.ValidAck(a) {
		return ErrBadAck
	}
	f.w.ApplyAck(a)
	return nil
}

// RecvWindow 应用窗口通告 w；w 为负整体失败，状态不变。
// 非零收缩被忽略、零窗口被接受，这两种都不算错误。
func (f *Flow) RecvWindow(w int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.w.ValidWindow(w) {
		return ErrBadWindow
	}
	f.w.ApplyWindow(w)
	return nil
}

// Snapshot 原子地返回五个只读视图值，供 api 的各 getter 使用。
func (f *Flow) Snapshot() (una, next, wnd, right, avail int64) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.w.Una(), f.w.Next(), f.w.Wnd(), f.w.Right(), f.w.Avail()
}
