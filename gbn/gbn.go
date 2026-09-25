// Package gbn 在 snd 窗口状态机之上施加累计 ACK 处理、互斥串行化与
// Go-Back-N 超时重传的结果收集。它只依赖 snd，不反向依赖。
package gbn

import (
	"errors"
	"sync"

	"ontology/snd"
)

// 三类可判定哨兵错误，彼此不同；前两类透传 snd 的同值哨兵，保证 errors.Is 成立。
var (
	// ErrBadWindow：W 非正（构造期拒绝）。
	ErrBadWindow = snd.ErrBadWindow
	// ErrWindowFull：窗口已满仍 Send。
	ErrWindowFull = snd.ErrWindowFull
	// ErrBadAck：ACK 序号为负。
	ErrBadAck = errors.New("gbn: negative acknowledgement")
)

// Sender 是受互斥保护的单连接 Go-Back-N 发送端。所有方法可被并发调用；
// 互斥保证任一读取拿到的 base/next/未确认集合来自同一个串行化时刻。
type Sender struct {
	mu sync.Mutex
	w  *snd.Window
}

// New 创建窗口大小为 W 的发送端；W 非正返回 ErrBadWindow，不留任何状态。
func New(W int) (*Sender, error) {
	w, err := snd.New(W)
	if err != nil {
		return nil, err
	}
	return &Sender{w: w}, nil
}

// Send 在窗口未满时发送段 next；满则返回 ErrWindowFull 且状态不变。
func (s *Sender) Send() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Send()
}

// Ack 处理累计 ACK a：负数在触碰任何状态之前被拒（ErrBadAck）；
// a<=base 为重复/乱序 ACK，由 snd 幂等忽略；a>base 时 base 单调推进到 a。
func (s *Sender) Ack(a int64) error {
	if a < 0 {
		return ErrBadAck
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w.Advance(a)
	return nil
}

// Timeout 触发一次 Go-Back-N：返回当前全部未确认段 [base,next) 的有序副本，
// 无未确认段返回 nil。base/next 不变，段对象视为重新计时。
func (s *Sender) Timeout() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Timeout()
}

// Base 返回已确认下界（序号 < base 的段均已被累计 ACK 覆盖）。
func (s *Sender) Base() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Base()
}

// Next 返回下一个待发送段序号。
func (s *Sender) Next() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Next()
}

// Unacked 返回未确认段的有序切片副本 [base,next)；空时返回 nil。
func (s *Sender) Unacked() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Unacked()
}

// AdvanceCheckConstant 透传 snd 的复杂度自检：多档 m 下一次 Ack(m) 推进到底，
// 内部检查段数恒为 0（不随 m 增长）。只回布尔，计数器数值不经任何导出签名外泄。
func AdvanceCheckConstant() bool { return snd.SelfCheckAdvanceConstant() }
