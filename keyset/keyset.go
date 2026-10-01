package keyset

import (
	"sort"
	"sync"
)

// Mode 是记录锁的模式。
type Mode int

const (
	// Shared 共享锁，与共享兼容。
	Shared Mode = iota
	// Exclusive 排他锁，与任何模式都不兼容。
	Exclusive
)

// valid 报告模式是否为共享或排他。
func (m Mode) valid() bool { return m == Shared || m == Exclusive }

// String 返回模式的可读名称。
func (m Mode) String() string {
	switch m {
	case Shared:
		return "共享"
	case Exclusive:
		return "排他"
	default:
		return "非法"
	}
}

// txState 是事务的生命周期状态。
type txState int

const (
	txActive txState = iota
	txCommitted
	txAborted
)

// gapLock 是键值开区间 (lo, hi) 上的空隙锁，端点在加锁时固定。
// loInf / hiInf 为 true 时分别表示负无穷 / 正无穷端点。
type gapLock struct {
	tx           int64
	lo, hi       int64
	loInf, hiInf bool
}

// contains 报告键 k 是否落在开区间 (lo, hi) 内。
func (g gapLock) contains(k int64) bool {
	if !g.loInf && k <= g.lo {
		return false
	}
	if !g.hiInf && k >= g.hi {
		return false
	}
	return true
}

// txInfo 记录一个事务的状态。
type txInfo struct {
	state    txState
	inserted []int64 // 该事务插入的键，中止时撤销
}

// Set 是基于邻键锁的有序整数键集合，所有方法均可并发调用。
type Set struct {
	mu      sync.Mutex
	keys    []int64                  // 升序、互不相同
	recLock map[int64]map[int64]Mode // 键 -> 事务 -> 记录锁模式
	gaps    []gapLock                // 空隙锁，彼此永不冲突
	txs     map[int64]*txInfo
	nextTx  int64
}

// NewSet 以互不相同的初始键创建集合；键重复时返回错误。
func NewSet(initialKeys ...int64) (*Set, error) {
	keys := append([]int64(nil), initialKeys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for i := 1; i < len(keys); i++ {
		if keys[i] == keys[i-1] {
			return nil, &Error{Kind: ErrKeyExists, Key: keys[i]}
		}
	}
	return &Set{
		keys:    keys,
		recLock: make(map[int64]map[int64]Mode),
		txs:     make(map[int64]*txInfo),
	}, nil
}

// Begin 开启一个事务并返回其 ID。
func (s *Set) Begin() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTx++
	s.txs[s.nextTx] = &txInfo{}
	return s.nextTx
}

// Scan 扫描 [lo, hi]（含端点），对范围内每个现存键加 mode 记录锁，
// 并对开区间 (p, s) 加空隙锁；返回范围内现存键的有序副本。
// 任一键的记录锁与他人冲突则整个扫描失败，不留任何锁。
func (s *Set) Scan(tx int64, lo, hi int64, mode Mode) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return nil, err
	}
	if !mode.valid() {
		return nil, &Error{Kind: ErrInvalidMode, Tx: tx, Mode: mode}
	}
	if lo > hi {
		return nil, &Error{Kind: ErrRangeReversed, Tx: tx, Lo: lo, Hi: hi}
	}
	// 范围内现存键：[loIdx, hiIdx)。
	loIdx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= lo })
	hiIdx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] > hi })
	// 先整体校验相容性，再统一加锁，保证冲突时不留任何锁。
	for _, k := range s.keys[loIdx:hiIdx] {
		if holders, ok := s.compatible(tx, k, mode); !ok {
			return nil, &Error{Kind: ErrRecordConflict, Tx: tx, Key: k, Holders: holders}
		}
	}
	for _, k := range s.keys[loIdx:hiIdx] {
		s.lockRecord(tx, k, mode)
	}
	// p 是小于 lo 的最大现存键，s 是大于 hi 的最小现存键。
	gap := gapLock{tx: tx, loInf: loIdx == 0, hiInf: hiIdx == len(s.keys)}
	if !gap.loInf {
		gap.lo = s.keys[loIdx-1]
	}
	if !gap.hiInf {
		gap.hi = s.keys[hiIdx]
	}
	s.gaps = append(s.gaps, gap)
	return append([]int64(nil), s.keys[loIdx:hiIdx]...), nil
}

// Get 点读键 k：存在则只加 mode 记录锁并返回 true；
// 不存在则只加 k 所在的 (p, s) 空隙锁并返回 false。
func (s *Set) Get(tx int64, key int64, mode Mode) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return false, err
	}
	if !mode.valid() {
		return false, &Error{Kind: ErrInvalidMode, Tx: tx, Mode: mode}
	}
	idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= key })
	if idx < len(s.keys) && s.keys[idx] == key {
		if holders, ok := s.compatible(tx, key, mode); !ok {
			return false, &Error{Kind: ErrRecordConflict, Tx: tx, Key: key, Holders: holders}
		}
		s.lockRecord(tx, key, mode)
		return true, nil
	}
	// 未命中：p、s 为 k 的现存前驱与后继，只加 (p, s) 空隙锁。
	gap := gapLock{tx: tx, loInf: idx == 0, hiInf: idx == len(s.keys)}
	if !gap.loInf {
		gap.lo = s.keys[idx-1]
	}
	if !gap.hiInf {
		gap.hi = s.keys[idx]
	}
	s.gaps = append(s.gaps, gap)
	return false, nil
}

// Insert 插入键 k 并令插入者持 k 的排他记录锁。
// k 已存在报「键已存在」；k 落在他人持有的任一空隙锁内报「间隙被占」。
func (s *Set) Insert(tx int64, key int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return err
	}
	idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= key })
	if idx < len(s.keys) && s.keys[idx] == key {
		return &Error{Kind: ErrKeyExists, Tx: tx, Key: key}
	}
	var holders []int64
	for _, g := range s.gaps {
		if g.tx != tx && g.contains(key) {
			holders = append(holders, g.tx)
		}
	}
	if len(holders) > 0 {
		return &Error{Kind: ErrGapOccupied, Tx: tx, Key: key, Holders: sortHolders(holders)}
	}
	s.keys = append(s.keys, 0)
	copy(s.keys[idx+1:], s.keys[idx:])
	s.keys[idx] = key
	s.lockRecord(tx, key, Exclusive)
	info := s.txs[tx]
	info.inserted = append(info.inserted, key)
	return nil
}

// Commit 提交事务并释放其全部锁。
func (s *Set) Commit(tx int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return err
	}
	s.releaseLocks(tx)
	s.txs[tx].state = txCommitted
	return nil
}

// Abort 中止事务，释放其全部锁并撤销它的全部插入。
func (s *Set) Abort(tx int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return err
	}
	info := s.txs[tx]
	s.releaseLocks(tx)
	// 撤销插入：这些键上的排他记录锁由本事务持有，他人不可能加锁。
	for _, k := range info.inserted {
		idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= k })
		if idx < len(s.keys) && s.keys[idx] == k {
			s.keys = append(s.keys[:idx], s.keys[idx+1:]...)
		}
		delete(s.recLock, k)
	}
	info.inserted = nil
	info.state = txAborted
	return nil
}

// compatible 校验 tx 以 mode 对 key 加记录锁是否与他人相容；
// 不相容时返回持锁的他事务（升序去重）。调用方须持有锁。
func (s *Set) compatible(tx, key int64, mode Mode) ([]int64, bool) {
	holders := s.recLock[key]
	var conflict []int64
	for other, m := range holders {
		if other == tx {
			continue
		}
		if mode == Exclusive || m == Exclusive {
			conflict = append(conflict, other)
		}
	}
	if len(conflict) > 0 {
		return sortHolders(conflict), false
	}
	return nil, true
}

// lockRecord 为 tx 在 key 上加 mode 记录锁；共享升级为排他时保留排他。
// 调用方须先通过 compatible 校验并持有锁。
func (s *Set) lockRecord(tx, key int64, mode Mode) {
	holders := s.recLock[key]
	if holders == nil {
		holders = make(map[int64]Mode)
		s.recLock[key] = holders
	}
	if holders[tx] == Exclusive {
		return
	}
	holders[tx] = mode
}

// releaseLocks 释放 tx 持有的全部记录锁与空隙锁。调用方须持有锁。
func (s *Set) releaseLocks(tx int64) {
	for key, holders := range s.recLock {
		if _, ok := holders[tx]; ok {
			delete(holders, tx)
			if len(holders) == 0 {
				delete(s.recLock, key)
			}
		}
	}
	gaps := s.gaps[:0]
	for _, g := range s.gaps {
		if g.tx != tx {
			gaps = append(gaps, g)
		}
	}
	s.gaps = gaps
}

// checkTx 校验事务存在且处于活跃状态；调用方须持有锁。
// 校验顺序：不存在 -> 已提交 -> 已中止。
func (s *Set) checkTx(tx int64) error {
	info, ok := s.txs[tx]
	if !ok {
		return &Error{Kind: ErrTxNotFound, Tx: tx}
	}
	switch info.state {
	case txCommitted:
		return &Error{Kind: ErrTxCommitted, Tx: tx}
	case txAborted:
		return &Error{Kind: ErrTxAborted, Tx: tx}
	}
	return nil
}
