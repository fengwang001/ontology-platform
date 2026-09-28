// Package temporal 实现流（事件流）与版本表之间的时态连接：
// 每个事件连接上其事件时间那一刻生效的版本，并在版本变更晚到、
// 水位线推进、并发调用等场景下保持结果正确。
package temporal

import "errors"

// Event 是事件流中的一条记录。Key 为连接键，EventTime 为事件时间。
type Event struct {
	Key       string
	EventTime int64
	Payload   string
}

// Version 是版本表中的一条版本记录。
// 版本区间为 [EffectiveAt, NextEffectiveAt)，最后一个版本到 +Inf。
// Tombstone 为 true 时表示该键在区间内无值（墓碑）。
type Version struct {
	Key         string
	EffectiveAt int64
	Tombstone   bool
	Value       string
}

// Status 表示一次事件提交的即时处理状态。
type Status int

const (
	// StatusResolved 表示事件在提交时即可确定连接结果。
	StatusResolved Status = iota
	// StatusBuffered 表示事件被缓冲，待水位线推进后输出。
	StatusBuffered
)

// ResultKind 表示连接结果类别。
type ResultKind int

const (
	// KindHit 命中一个非墓碑版本。
	KindHit ResultKind = iota
	// KindMiss 未命中：不存在生效起点不大于事件时间的版本，或该版本为墓碑。
	KindMiss
)

// Result 是一个事件最终确定的连接输出。每个被接受的事件恰好产生一个 Result。
type Result struct {
	// Seq 是事件被接受时的全局先后序号，从 0 开始。
	Seq int
	// Kind 是命中 / 未命中。
	Kind ResultKind
	// Key、EventTime、Payload 回填自输入事件。
	Key       string
	EventTime int64
	Payload   string
	// EffectiveAt 是命中版本的生效起点；Kind==KindMiss 时无意义。
	EffectiveAt int64
	// Value 是命中版本的值；Kind==KindMiss 时为空。
	Value string
}

// 可区分的拒绝原因。被拒绝的操作不会改变任何内部状态。
var (
	// ErrEmptyKey：事件或版本变更的键为空。
	ErrEmptyKey = errors.New("temporal: empty key")
	// ErrLateVersion：版本变更晚到，其生效起点不大于当前水位线。
	ErrLateVersion = errors.New("temporal: late version change (effectiveAt <= watermark)")
	// ErrWatermarkRegression：新水位线小于当前水位线（水位线回退）。
	ErrWatermarkRegression = errors.New("temporal: watermark regression")
	// ErrBufferLimitExceeded：事件需要缓冲但缓冲已达上限。
	ErrBufferLimitExceeded = errors.New("temporal: buffered event limit exceeded")
)
