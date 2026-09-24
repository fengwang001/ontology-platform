// Package seg 维护单个会话的内部状态：按 Seq 收集事件、缺口/越界判定、
// 按 Seq 升序折叠结果以及关闭判定。它不依赖其他包，自身不加锁（由 mrg 统一加锁）。
package seg

import (
	"errors"
	"sort"
)

// 可判定的哨兵错误（四类故障中的三类产生于此层）。
var (
	// ErrInvalid：序号或取值非法（Seq <= 0，或 Value 不在 [0,9]）。
	ErrInvalid = errors.New("seg: invalid seq or value")
	// ErrConflict：同一 Seq 已有不同 Value。
	ErrConflict = errors.New("seg: seq conflict")
	// ErrIncomplete：Close 时 1..N 有缺口或存在 Seq > N 的越界项。
	ErrIncomplete = errors.New("seg: session incomplete")
	// ErrClosed：会话已关闭后的 Append，或以不同 N 重复 Close。
	ErrClosed = errors.New("seg: session closed")
)

// Seg 是单个会话的状态。零值不可用，必须用 New 创建。
type Seg struct {
	values map[int]int // seq -> value，仅在未关闭时写入
	closed bool
	n      int // 关闭时的 N
	result int // 关闭时冻结的折叠结果
}

// New 创建一个空会话。
func New() *Seg {
	return &Seg{values: make(map[int]int)}
}

// Append 记录一条事件。
// 已关闭一律拒绝；Seq<=0 或 Value 越界拒绝；重复 Seq 同值幂等、异值冲突。
// 任何拒绝都发生在写入之前，因此不会改变状态。
func (s *Seg) Append(seq, value int) error {
	if s.closed {
		return ErrClosed
	}
	if seq <= 0 || value < 0 || value > 9 {
		return ErrInvalid
	}
	if v, ok := s.values[seq]; ok {
		if v != value {
			return ErrConflict
		}
		return nil // 幂等：同 Seq 同 Value
	}
	s.values[seq] = value
	return nil
}

// Close 在 Seq=1..n 恰好全部出现且无越界项时成功：按 Seq 升序折叠并冻结结果。
// 已关闭时同 N 幂等成功、不同 N 拒绝；不完整时不改任何状态。
func (s *Seg) Close(n int) error {
	if s.closed {
		if s.n == n {
			return nil
		}
		return ErrClosed
	}
	if n <= 0 || len(s.values) != n {
		return ErrIncomplete // N 非正、有缺口或有越界项
	}
	r := 0
	for seq := 1; seq <= n; seq++ {
		v, ok := s.values[seq]
		if !ok {
			return ErrIncomplete // 缺口；此时尚未写任何字段
		}
		r = r*10 + v
	}
	// len==n 且 1..n 全部存在，故不可能存在 >n 的键。
	s.closed = true
	s.n = n
	s.result = r
	return nil
}

// Result 返回冻结结果；未关闭时 ok 为 false。
func (s *Seg) Result() (value int, ok bool) {
	if !s.closed {
		return 0, false
	}
	return s.result, true
}

// Closed 报告会话是否已关闭。
func (s *Seg) Closed() bool { return s.closed }

// Seen 返回已见 Seq 的升序副本（诊断用，只读）。
func (s *Seg) Seen() []int {
	out := make([]int, 0, len(s.values))
	for seq := range s.values {
		out = append(out, seq)
	}
	sort.Ints(out)
	return out
}
