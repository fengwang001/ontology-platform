// Package api 对外接口。依赖 cc。
package api

import (
	"errors"
	"fmt"

	"ontology/cc"
	"ontology/cwnd"
)

// State 阶段类型与常量，自 cwnd 再导出。
type State = cwnd.State

const (
	SlowStart = cwnd.SlowStart
	CongAvoid = cwnd.CongAvoid
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadParam     = cc.ErrBadParam
	ErrCwndLimit    = cc.ErrCwndLimit
	ErrAlreadyFloor = cc.ErrAlreadyFloor
)

// Sender 是发送方拥塞控制句柄，并发安全。
type Sender struct{ c *cc.Controller }

// New 构造：ssthresh>=2、maxCwnd>=1，否则 ErrBadParam。
func New(ssthresh, maxCwnd int64) (*Sender, error) {
	c, err := cc.New(ssthresh, maxCwnd)
	if err != nil {
		return nil, err
	}
	return &Sender{c: c}, nil
}

func (s *Sender) OnAck() error  { return s.c.OnAck() }
func (s *Sender) OnLoss() error { return s.c.OnLoss() }

func (s *Sender) Cwnd() int64     { cw, _, _, _ := s.c.Snapshot(); return cw }
func (s *Sender) Ssthresh() int64 { _, ss, _, _ := s.c.Snapshot(); return ss }
func (s *Sender) State() State    { _, _, _, st := s.c.Snapshot(); return st }

// Partial 返回拥塞避免的部分 ACK 计数（供演示与自检逐步核对）。
func (s *Sender) Partial() int64 { _, _, p, _ := s.c.Snapshot(); return p }

// VerifyAckCost 只回报谓词：加性增每次 OnAck 检查的 ACK 记录数 <= 1。
func (s *Sender) VerifyAckCost() bool { return s.c.VerifyAckCost() }

// SelfCheck 对内置 ACK/丢包序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量 1/2/3：八步序列逐步对拍（AIMD + 左闭切换 + 减半）
	s, err := New(4, 1<<40)
	if err != nil {
		return err
	}
	type row struct{ cw, ss, p int64 }
	want := []row{{2, 4, 0}, {3, 4, 0}, {4, 4, 0}, {4, 4, 1}, {4, 4, 2}, {4, 4, 3}, {5, 4, 0}}
	for i, w := range want {
		if err := s.OnAck(); err != nil {
			return fmt.Errorf("selfcheck 步%d: %w", i+1, err)
		}
		if s.Cwnd() != w.cw || s.Ssthresh() != w.ss || s.Partial() != w.p {
			return fmt.Errorf("selfcheck 步%d: got (%d,%d,%d)", i+1, s.Cwnd(), s.Ssthresh(), s.Partial())
		}
		if i < 3 && s.State() != SlowStart || i >= 3 && s.State() != CongAvoid {
			return fmt.Errorf("selfcheck 步%d: state=%s", i+1, s.State())
		}
	}
	if err := s.OnLoss(); err != nil {
		return err
	}
	if s.Cwnd() != 1 || s.Ssthresh() != 2 || s.State() != SlowStart || s.Partial() != 0 {
		return errors.New("selfcheck: OnLoss 减半错误")
	}
	// 不变量 4：失败不留痕
	if _, err := New(1, 5); !errors.Is(err, ErrBadParam) {
		return errors.New("selfcheck: ErrBadParam 缺失")
	}
	s2, _ := New(2, 2)
	if err := s2.OnAck(); !errors.Is(err, ErrCwndLimit) {
		return errors.New("selfcheck: ErrCwndLimit 缺失")
	}
	if s2.Cwnd() != 1 || s2.State() != SlowStart || s2.Partial() != 0 {
		return errors.New("selfcheck: 拒绝后状态被改")
	}
	if err := s2.OnLoss(); !errors.Is(err, ErrAlreadyFloor) {
		return errors.New("selfcheck: ErrAlreadyFloor 缺失")
	}
	if !s.VerifyAckCost() {
		return errors.New("selfcheck: 加性增检查记录数 > 1")
	}
	return nil
}
