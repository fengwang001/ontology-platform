// Package txid 提供单调事务号的分配与比较。
//
// 事务号只由注入的分配器产生，代码中不存在时钟或随机来源。
// 零值事务号非法，它被保留为“没有事务号”的哨兵。
package txid

import "errors"

// TxID 是一次事务的全局事务号。零值非法。
type TxID uint64

// IsValid 报告该事务号是否非零。
func (t TxID) IsValid() bool { return t != 0 }

// Less 严格小于比较。
func (t TxID) Less(o TxID) bool { return t < o }

// ErrExhausted 表示单调事务号空间已耗尽，分配器不得回绕。
var ErrExhausted = errors.New("txid: transaction id space exhausted")

// Source 是单调事务号的唯一来源。零值 Source 不可用，必须用 NewSource 构造。
type Source struct {
	next uint64
}

// NewSource 创建一个从 1 开始分配的事务号源。
func NewSource() *Source { return &Source{next: 1} }

// Next 分配下一个严格更大的事务号。单调递增、永不回绕。
func (s *Source) Next() (TxID, error) {
	if s == nil {
		return 0, ErrExhausted
	}
	if s.next == 0 { // uint64 溢出后停在零，拒绝继续分配。
		return 0, ErrExhausted
	}
	id := s.next
	s.next++
	return TxID(id), nil
}

// Last 返回最近一次已分配的事务号；从未分配时返回零值（非法）。
func (s *Source) Last() TxID {
	if s == nil || s.next <= 1 {
		return 0
	}
	return TxID(s.next - 1)
}

// Frontier 返回“下一个将被分配的号”。
// 快照建立时以它作为快照点：恰好等于快照点的提交尚未分配，必然不可见。
func (s *Source) Frontier() TxID {
	if s == nil {
		return 1
	}
	return TxID(s.next)
}
