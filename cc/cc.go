// Package cc 执行拥塞控制事件 OnAck/OnLoss 并做错误判定。依赖 cwnd。
package cc

import (
	"errors"
	"sync"

	"ontology/cwnd"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadParam     = errors.New("cc: ssthresh<2 或 maxCwnd<1")
	ErrCwndLimit    = errors.New("cc: cwnd 达到 maxCwnd 上限")
	ErrAlreadyFloor = errors.New("cc: cwnd 已触底(=1)")
)

// Controller 推进拥塞控制事件，并发安全。
type Controller struct {
	mu sync.RWMutex
	w  *cwnd.Window
}

// New 校验参数：ssthresh>=2 且 maxCwnd>=1，否则 ErrBadParam。
func New(ssthresh, maxCwnd int64) (*Controller, error) {
	if ssthresh < 2 || maxCwnd < 1 {
		return nil, ErrBadParam
	}
	return &Controller{w: cwnd.New(ssthresh, maxCwnd)}, nil
}

// OnAck 一个段被确认。若应用后 cwnd 会达到 maxCwnd，则整体拒绝
// （ErrCwndLimit），cwnd/ssthresh/state/partial 均不变。
func (c *Controller) OnAck() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if nc, _, _ := c.w.AckPreview(); nc >= c.w.MaxCwnd() {
		return ErrCwndLimit
	}
	c.w.ApplyAck()
	return nil
}

// OnLoss 丢包（超时）。cwnd 已触底则拒绝（ErrAlreadyFloor），状态不变。
func (c *Controller) OnLoss() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.w.AtFloor() {
		return ErrAlreadyFloor
	}
	c.w.ApplyLoss()
	return nil
}

// Snapshot 返回一致性快照 (cwnd, ssthresh, partial, state)。
func (c *Controller) Snapshot() (cw, ss, p int64, st cwnd.State) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.w.Cwnd(), c.w.Ssthresh(), c.w.Partial(), c.w.State()
}

// VerifyAckCost 只回报谓词：加性增每次 OnAck 检查的 ACK 记录数 <= 1。
func (c *Controller) VerifyAckCost() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.w.VerifyAckCost()
}
