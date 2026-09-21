// Package shard 维护分片余额状态，支持按 Txn 幂等地应用 WAL 记录。
package shard

import (
	"sync"

	"ontology/wal"
)

// Set 是 n 个分片的余额集合。所有方法并发安全。
type Set struct {
	mu       sync.Mutex
	balances []int64
	lastTxn  []uint64 // 每片已应用的最大 Txn，用于幂等跳过
}

// New 创建 n 个分片，每片初始余额为 initial。
func New(n int, initial int64) *Set {
	balances := make([]int64, n)
	for i := range balances {
		balances[i] = initial
	}
	return &Set{
		balances: balances,
		lastTxn:  make([]uint64, n),
	}
}

// Len 返回分片数量。
func (s *Set) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.balances)
}

// Apply 应用一条记录。若该片的 LastTxn 已不小于 r.Txn（即应用过），
// 则跳过并返回 false；否则累加余额、推进 LastTxn 并返回 true。
func (s *Set) Apply(r wal.Record) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Shard < 0 || r.Shard >= len(s.balances) {
		return false
	}
	// 幂等判定必须按片比较 lastTxn[r.Shard]；用全局最大 Txn 会把
	// 同一 Txn 的另一半（落在别的片上）误判为已应用而丢弃。
	if r.Txn <= s.lastTxn[r.Shard] {
		return false
	}
	s.balances[r.Shard] += r.Delta
	s.lastTxn[r.Shard] = r.Txn
	return true
}

// Balance 返回第 i 片的余额；i 越界时返回 0。
func (s *Set) Balance(i int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.balances) {
		return 0
	}
	return s.balances[i]
}

// Total 返回所有分片余额之和。
func (s *Set) Total() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, b := range s.balances {
		total += b
	}
	return total
}

// LastTxn 返回第 i 片已应用的最大 Txn；i 越界时返回 0。
func (s *Set) LastTxn(i int) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.lastTxn) {
		return 0
	}
	return s.lastTxn[i]
}
