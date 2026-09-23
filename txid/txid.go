// Package txid 提供单调递增、不回绕、零值非法的事务号。
// 事务号只能由注入的 Source 分配，代码中不存在其他时间/时钟来源。
package txid

import "sync"

// TXID 是事务的全序标识；零值保留为"尚未分配"的非法值。
type TXID uint64

// Valid 报告事务号是否已分配（非零）。
func (t TXID) Valid() bool { return t != 0 }

// Before 严格小于比较，即提交序上的先后关系。
func (t TXID) Before(o TXID) bool { return t < o }

// Source 是事务号的唯一注入源。零值 Source 从 1 开始发号。
type Source struct {
	mu  sync.Mutex
	next TXID
}

// Next 分配并返回下一个事务号；uint64 用尽时 panic（不回绕）。
func (s *Source) Next() TXID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == 0 {
		s.next = 1
	}
	if s.next == ^TXID(0) {
		panic("txid: source exhausted")
	}
	v := s.next
	s.next++
	return v
}

// Peek 返回"即将发出的下一个号"但不分配；零值 Source 视同为 1。
func (s *Source) Peek() TXID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == 0 {
		return 1
	}
	return s.next
}
