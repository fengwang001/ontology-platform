// Package api 是双输入屏障对齐算子的对外接口。
// 依赖方向：api -> align -> chanbuf。并发安全由 align 的读写锁保证，
// Snapshot/State/SelfCheck 可被多个 goroutine 并发调用。
package api

import (
	"ontology/align"
	"ontology/chanbuf"
)

// 对外类型即底层类型别名。
type (
	Item = chanbuf.Item
	Out  = chanbuf.Out
	Kind = chanbuf.Kind
)

const (
	KindRecord  = chanbuf.KindRecord
	KindBarrier = chanbuf.KindBarrier
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrInvalidElement = align.ErrInvalidElement
	ErrBarrierOrder   = align.ErrBarrierOrder
	ErrBufferLimit    = align.ErrBufferLimit
)

// Operator 是并发安全的两通道对齐求和算子。
type Operator struct{ a *align.Aligner }

// New 创建算子，maxBuffered 为两通道缓冲元素总数上限。
func New(maxBuffered int) *Operator { return &Operator{a: align.New(maxBuffered)} }

// Push 按顺序处理一批元素，返回本批产生的输出流片段。
// 任一条被拒则整批不生效，且算子之后仍可正常使用。
func (o *Operator) Push(items []Item) ([]Out, error) { return o.a.Push(items) }

// Snapshot 返回检查点 n 的快照拷贝；不存在时 ok=false。
func (o *Operator) Snapshot(n int64) (map[string]int64, bool) { return o.a.Snapshot(n) }

// State 返回当前 sum 的拷贝（等于输出流中全部记录按 Key 求和）。
func (o *Operator) State() map[string]int64 { return o.a.State() }

// SelfCheck 用内置输入序列核验四条不变量（切割一致、通道内保序、
// 不丢不重、失败不留痕），全部成立返回 true。
func (o *Operator) SelfCheck() bool { return o.a.SelfCheck() }
