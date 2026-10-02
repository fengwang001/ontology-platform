// Package batchisolate 提供带二分隔离与已知毒丸表的批写入隔离器。
//
// 面对“整批原子成功或失败”的 sink，隔离器在整批失败时区分瞬时与永久
// 错误：瞬时错误在重试预算内重试；永久错误时对批次执行二分隔离，递归
// 定位毒丸记录并送入死信，其余记录保持原序恰好交付一次。详见同包
// DESIGN.md。
package batchisolate

import (
	"sync"
	"sync/atomic"
)

// Reason 是死信原因。
type Reason string

const (
	// Known 表示记录在 Submit 开始时已存在于已知毒丸表快照中，直接摘出。
	Known Reason = "Known"
	// Poison 表示记录经永久错误（含 certain 推断）确认为毒丸。
	Poison Reason = "Poison"
	// Exhausted 表示唯一记录在 R+1 次瞬时错误后仍无法交付（预算未耗尽）。
	Exhausted Reason = "Exhausted"
	// Budget 表示 Submit 的 sink 调用预算 Cmax 耗尽，记录尚未裁决。
	Budget Reason = "Budget"
)

// DeadLetter 是一条死信记录，ID 为原始编号，Reason 为死信原因。
type DeadLetter struct {
	ID     string
	Reason Reason
}

// SinkFunc 是注入的整批原子写入函数：要么整批成功（返回 nil），
// 要么整批失败（返回非 nil 错误，调用方不得假定任何记录被交付）。
// ids 为非空切片。同一 Submit 内的调用串行，不同 Submit 之间可能并行，
// 实现必须自身保证并发安全（若需要）。
type SinkFunc func(ids []string) error

// Result 是一次 Submit 的结果。
type Result struct {
	// Delivered 按原始入参顺序列出成功交付的编号。
	Delivered []string
	// Dead 按原始入参顺序列出死信编号及其原因。
	Dead []DeadLetter
	// Calls 是本次 Submit 实际发起的 sink 调用总次数（瞬时重试也计数）。
	Calls int
}

// Isolator 是批写入隔离器。零值不可用，必须通过 New 构造。
type Isolator struct {
	sink   SinkFunc
	r      int
	km     int
	cmax   int
	mu     sync.Mutex
	table  []string
	wg     sync.WaitGroup
	closed bool
	calls  atomic.Int64
}

// New 构造隔离器。
//
//   - retries：瞬时错误重试次数 R，范围 0..10，每个待处理批最多尝试 R+1 次。
//   - knownCap：已知毒丸表容量 Km，范围 0..1000；为 0 时不记录任何毒丸。
//   - budget：单次 Submit 的 sink 调用预算 Cmax，范围 1..1_000_000。
//   - sink：整批原子写入函数，不能为 nil。
func New(retries, knownCap, budget int, sink SinkFunc) (*Isolator, error) {
	if retries < 0 || retries > 10 {
		return nil, errInvalidArgs
	}
	if knownCap < 0 || knownCap > 1000 {
		return nil, errInvalidArgs
	}
	if budget < 1 || budget > 1_000_000 {
		return nil, errInvalidArgs
	}
	if sink == nil {
		return nil, errInvalidArgs
	}
	return &Isolator{sink: sink, r: retries, km: knownCap, cmax: budget}, nil
}

// Close 关闭隔离器。Close 之后的 Submit 返回关闭错误；Close 等待所有
// 在途 Submit 结束，可重复调用且幂等。
func (iso *Isolator) Close() error {
	iso.mu.Lock()
	if !iso.closed {
		iso.closed = true
	}
	iso.mu.Unlock()
	iso.wg.Wait()
	return nil
}

// Known 返回当前已知毒丸表的快照，顺序为追加先后（FIFO）。
func (iso *Isolator) Known() []string {
	iso.mu.Lock()
	defer iso.mu.Unlock()
	return append([]string(nil), iso.table...)
}

// Calls 返回隔离器自构造以来所有 Submit 发起的 sink 调用总次数。
func (iso *Isolator) Calls() int64 {
	return iso.calls.Load()
}
