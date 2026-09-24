// Package api 是事件乱序度观测器的对外门面：构造、喂事件、读四项统计、
// 冻结与自检。所有方法并发安全。
package api

import (
	"ontology/obs"
)

// 三类可判定哨兵错误，与 obs 中同一实例，可直接 errors.Is(err, api.ErrXxx)。
var (
	ErrInvalidSeq    = obs.ErrInvalidSeq    // 序号非法（Seq <= 0）
	ErrFrozen        = obs.ErrFrozen        // Freeze 之后再次写
	ErrNegativeSlack = obs.ErrNegativeSlack // New 的 maxSlack 为负
)

// Observer 对外观测器；内部组合 obs.Observer。
type Observer struct {
	o *obs.Observer
}

// New 以 maxSlack 构造观测器；maxSlack 为负返回 ErrNegativeSlack。
func New(maxSlack int64) (*Observer, error) {
	o, err := obs.New(maxSlack)
	if err != nil {
		return nil, err
	}
	return &Observer{o: o}, nil
}

// Feed 观测一个序号为 seq 的事件（纯观测，绝不丢弃）。
func (x *Observer) Feed(seq int64) error { return x.o.Feed(seq) }

// MaxSeen 返回迄今最大 Seq。
func (x *Observer) MaxSeen() int64 { return x.o.MaxSeen() }

// OutOfOrder 返回累计乱序事件数。
func (x *Observer) OutOfOrder() int64 { return x.o.OutOfOrder() }

// MaxLateness 返回历次乱序迟到量的历史最大值。
func (x *Observer) MaxLateness() int64 { return x.o.MaxLateness() }

// ExceedsSlack 返回是否出现过迟到量 > maxSlack 的乱序事件。
func (x *Observer) ExceedsSlack() bool { return x.o.ExceedsSlack() }

// Freeze 冻结观测器，此后 Feed 与重复 Freeze 返回 ErrFrozen。
func (x *Observer) Freeze() error { return x.o.Freeze() }

// SelfCheck 运行内置自检，核验第二节四条不变量与 O(1) 复杂度，全过返回 nil。
func (x *Observer) SelfCheck() error { return x.o.SelfCheck() }
