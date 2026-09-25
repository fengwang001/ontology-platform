// Package spin 在 tick.Counters 之上实现自旋等待、指数退避（翻倍、封顶、
// 成功后重置为 1）与带 deadline 的 TryAcquire。只依赖 tick 包。
package spin

import (
	"errors"
	"time"

	"ontology/tick"
)

// ErrTimeout 是 TryAcquire 在 deadline 内未获锁的可判定哨兵错误。
var ErrTimeout = errors.New("ticklock: acquire timed out")

// initialBackoff 是每次自旋的第一个暂停时长（ns），获锁成功后重置回它。
const initialBackoff = time.Duration(1)

// Spinner 绑定一组 ticket 计数器与一个退避上限。
type Spinner struct {
	c   *tick.Counters
	max time.Duration
}

// New 创建绑定 c 的自旋器，maxBackoff 为暂停上限（调用方保证为正）。
func New(c *tick.Counters, maxBackoff time.Duration) *Spinner {
	return &Spinner{c: c, max: maxBackoff}
}

// NewOwn 创建自持一组计数器的自旋器，供不需要直接触碰 tick 的上层使用，
// 从而保证依赖方向严格为 上层 → spin → tick。
func NewOwn(maxBackoff time.Duration) *Spinner {
	return New(tick.New(), maxBackoff)
}

// Handover 是 Release 的底层转发：仅当 ticket==serving 时把 serving +1，
// 号不符返回 false 且不写计数器（失败不留痕）。
func (s *Spinner) Handover(ticket int) bool { return s.c.Handover(ticket) }

// Next、Serving 原子读两个计数器（State 用），差值即等待者数。
func (s *Spinner) Next() int    { return s.c.Next() }
func (s *Spinner) Serving() int { return s.c.Serving() }

// grow 把 d 翻倍并封顶；nd<=0 即有符号翻倍溢出，按规则封到 max（不设上限
// 才会得到负值的忙自旋，见 NOTES 丙）。
func (s *Spinner) grow(d time.Duration) time.Duration {
	nd := d * 2
	if nd <= 0 || nd > s.max {
		return s.max
	}
	return nd
}

// Acquire 原子取号 t:=next;next++，自旋直到原子读 serving==t，返回 t。
// 每次「检查发现尚未轮到」后按 2 的幂暂停（1,2,4,…，上限 max），
// 获锁成功后退避重置为初始值 1。
func (s *Spinner) Acquire() int {
	t := s.c.Take()
	d := initialBackoff
	for !s.c.IsServing(t) { // 检查与暂停都走 serving 的原子读，不缓存旧值
		time.Sleep(d)
		d = s.grow(d)
	}
	return t
}

// TryAcquire 与 Acquire 相同但受 deadline 限制：只在锁无主且队列无人时
// 经 CAS 取号（tick.TryTakeFree），因此超时返回 ErrTimeout 时 next/serving
// 零变化，且绝不会插到已排队等待者前面（FIFO 不被破坏）。
func (s *Spinner) TryAcquire(deadline time.Duration) (int, error) {
	stop := time.Now().Add(deadline)
	d := initialBackoff
	for {
		if t, ok := s.c.TryTakeFree(); ok {
			for !s.c.IsServing(t) {
				time.Sleep(d)
				d = s.grow(d)
			}
			return t, nil
		}
		now := time.Now()
		if !now.Before(stop) {
			return 0, ErrTimeout
		}
		p := d
		if p > s.max {
			p = s.max
		}
		if rem := stop.Sub(now); p > rem { // 不越过 deadline
			p = rem
		}
		time.Sleep(p)
		d = s.grow(d)
	}
}
