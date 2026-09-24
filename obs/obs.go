// Package obs 维护事件乱序度统计：MaxSeen、OutOfOrder、MaxLateness、
// ExceedsSlack，并负责推进与冻结。观测器纯观测：乱序事件只计数，
// 绝不丢弃，MaxSeen 只被有序事件推进。本包依赖 seq。
package obs

import (
	"errors"
	"sync"

	"ontology/seq"
)

// 三类可判定哨兵错误，彼此互不相同；api 包对外原样暴露。
var (
	// ErrInvalidSeq：Feed 收到 Seq ≤ 0。
	ErrInvalidSeq = errors.New("obs: seq must be >= 1")
	// ErrFrozen：Freeze 之后再次 Feed。
	ErrFrozen = errors.New("obs: observer is frozen")
	// ErrInvalidSlack：New 的 maxSlack 为负。
	ErrInvalidSlack = errors.New("obs: maxSlack must be >= 0")
)

// Observer 是单实例乱序度观测器，所有方法并发安全。
type Observer struct {
	mu sync.Mutex

	slack int64

	seen     bool // 是否见过任何事件；false 时 maxSeen 等价负无穷
	maxSeen  int64
	outOrder int64 // 累计乱序事件个数
	maxLate  int64 // 乱序迟到量历史最大值
	exceeds  bool  // 是否出现过 late > maxSlack
	frozen   bool

	// lastChecks 记录最近一次成功 Feed 检查过的历史事件个数。
	// MaxSeen 由标量维护，每次 Feed 只与标量比较一次，故恒为 1，
	// 不随历史长度增长。非导出：外部任何途径都读不到。
	lastChecks int
}

// New 创建观测器；maxSlack 为负时整体失败，不产生任何状态。
func New(maxSlack int64) (*Observer, error) {
	if maxSlack < 0 {
		return nil, ErrInvalidSlack
	}
	return &Observer{slack: maxSlack}, nil
}

// Feed 喂入一个事件。非法序号或冻结后写入都在改状态之前被拒绝，
// 不留任何痕迹；成功时每个事件只与标量 MaxSeen 比较一次。
func (o *Observer) Feed(s int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.frozen {
		return ErrFrozen
	}
	if !seq.Valid(s) {
		return ErrInvalidSeq
	}
	if !o.seen {
		o.seen = true
		o.maxSeen = s // 首个事件有序
		o.lastChecks = 1
		return nil
	}
	switch seq.Classify(s, o.maxSeen) {
	case seq.InOrder:
		o.maxSeen = s // 只有有序事件推进 MaxSeen
	case seq.Equal:
		// 相等算有序：不计数、不推进
	case seq.Late:
		late := seq.Lateness(s, o.maxSeen)
		o.outOrder++ // 纯观测：照常计数，绝不丢弃
		if late > o.maxLate {
			o.maxLate = late
		}
		if late > o.slack {
			o.exceeds = true // 仍计数，仅置标志位
		}
	}
	o.lastChecks = 1
	return nil
}

// Freeze 冻结观测器；幂等，重复调用仍返回 nil，冻结态保持不变。
func (o *Observer) Freeze() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.frozen = true
	return nil
}

// MaxSeen 返回迄今最大序号；未见任何事件时返回 0（等价负无穷的占位值）。
func (o *Observer) MaxSeen() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.maxSeen
}

func (o *Observer) OutOfOrder() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.outOrder
}

func (o *Observer) MaxLateness() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.maxLate
}

func (o *Observer) ExceedsSlack() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.exceeds
}

// LastChecks 仅供 obs 包内白盒测试使用：非导出字段、非导出方法，
// 公开接口与 api 包均无法接触。
func (o *Observer) lastChecksCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastChecks
}
