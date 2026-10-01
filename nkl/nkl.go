// Package nkl 实现基于邻键锁（next-key locking）的有序整数键集合。
//
// 记录锁分共享（Shared）与排他（Exclusive），仅共享与共享兼容；
// 空隙锁记为键值开区间 (p, s)，彼此永不冲突，端点在加锁时固定。
// 扫描与点读会同时获取记录锁与空隙锁，从而避免同一事务重复扫描出现幻读。
package nkl

import (
	"fmt"
	"sort"
	"sync"
)

// TxID 事务标识。
type TxID uint64

// Mode 锁模式。
type Mode int

const (
	// Shared 共享锁。
	Shared Mode = iota
	// Exclusive 排他锁。
	Exclusive
)

// String 返回模式的中文名。
func (m Mode) String() string {
	switch m {
	case Shared:
		return "共享"
	case Exclusive:
		return "排他"
	}
	return fmt.Sprintf("无效模式(%d)", int(m))
}

// Kind 错误类别。
type Kind int

const (
	// ErrTxNotFound 事务不存在。
	ErrTxNotFound Kind = iota
	// ErrTxCommitted 事务已提交。
	ErrTxCommitted
	// ErrTxAborted 事务已中止。
	ErrTxAborted
	// ErrInvalidMode 模式不是共享或排他。
	ErrInvalidMode
	// ErrRangeReversed 范围颠倒（lo 大于 hi）。
	ErrRangeReversed
	// ErrLockConflict 记录锁冲突。
	ErrLockConflict
	// ErrKeyExists 键已存在。
	ErrKeyExists
	// ErrGapOccupied 间隙被占。
	ErrGapOccupied
)

// Error 操作被拒绝时返回的错误，携带判定依据。
type Error struct {
	Kind   Kind
	Tx     TxID // 发起操作的事务
	Holder TxID // 冲突 / 占隙时的持锁事务
	Key    int64
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	switch e.Kind {
	case ErrTxNotFound:
		return fmt.Sprintf("事务不存在: tx=%d", e.Tx)
	case ErrTxCommitted:
		return fmt.Sprintf("事务已提交: tx=%d", e.Tx)
	case ErrTxAborted:
		return fmt.Sprintf("事务已中止: tx=%d", e.Tx)
	case ErrInvalidMode:
		return fmt.Sprintf("模式无效（须为共享或排他）: tx=%d", e.Tx)
	case ErrRangeReversed:
		return fmt.Sprintf("范围颠倒（lo 大于 hi）: tx=%d", e.Tx)
	case ErrLockConflict:
		return fmt.Sprintf("记录锁冲突: tx=%d 键=%d 持锁事务=%d", e.Tx, e.Key, e.Holder)
	case ErrKeyExists:
		return fmt.Sprintf("键已存在: tx=%d 键=%d", e.Tx, e.Key)
	case ErrGapOccupied:
		return fmt.Sprintf("间隙被占: tx=%d 键=%d 持锁事务=%d", e.Tx, e.Key, e.Holder)
	}
	return "未知错误"
}

// gap 键值开区间 (p, s)，pInf/sInf 表示负无穷 / 正无穷端点。
type gap struct {
	p, s       int64
	pInf, sInf bool
}

// contains 判断 k 是否落在开区间 (p, s) 内。
func (g gap) contains(k int64) bool {
	return (g.pInf || k > g.p) && (g.sInf || k < g.s)
}

// gapLock 某事务持有的一道空隙锁。
type gapLock struct {
	tx  TxID
	gap gap
}

// txState 事务状态。
type txState int

const (
	txActive txState = iota
	txCommitted
	txAborted
)

// Set 基于邻键锁的有序整数键集合，所有方法可并发调用。
type Set struct {
	mu       sync.Mutex
	keys     []int64 // 升序排列的现存键
	rec      map[int64]map[TxID]Mode
	gaps     []gapLock
	txs      map[TxID]txState
	inserted map[TxID][]int64 // 各事务插入的键，用于中止时撤销
	nextTx   TxID
}

// NewSet 以互不相同的初始键创建集合。
func NewSet(initialKeys ...int64) *Set {
	keys := make([]int64, len(initialKeys))
	copy(keys, initialKeys)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return &Set{
		keys:     keys,
		rec:      make(map[int64]map[TxID]Mode),
		txs:      make(map[TxID]txState),
		inserted: make(map[TxID][]int64),
	}
}

// Begin 开启事务并返回其标识。
func (s *Set) Begin() TxID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTx++
	s.txs[s.nextTx] = txActive
	return s.nextTx
}

// Scan 扫描闭区间 [lo, hi]，对范围内每个现存键加 mode 记录锁，
// 并对开区间 (p, s) 加空隙锁；任一记录锁冲突则整体失败且不留任何锁。
func (s *Set) Scan(tx TxID, lo, hi int64, mode Mode) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return nil, err
	}
	if mode != Shared && mode != Exclusive {
		return nil, &Error{Kind: ErrInvalidMode, Tx: tx}
	}
	if lo > hi {
		return nil, &Error{Kind: ErrRangeReversed, Tx: tx}
	}
	start := searchInts(s.keys, lo)
	end := start
	for end < len(s.keys) && s.keys[end] <= hi {
		end++
	}
	// 先校验全部记录锁，冲突则整体失败、不留任何锁。
	for i := start; i < end; i++ {
		if holder, ok := s.recConflict(tx, s.keys[i], mode); ok {
			return nil, &Error{Kind: ErrLockConflict, Tx: tx, Holder: holder, Key: s.keys[i]}
		}
	}
	for i := start; i < end; i++ {
		s.lockRec(tx, s.keys[i], mode)
	}
	// p 为小于 lo 的最大现存键，s 为大于 hi 的最小现存键，端点在此刻固定。
	g := gap{pInf: start == 0, sInf: end == len(s.keys)}
	if start > 0 {
		g.p = s.keys[start-1]
	}
	if end < len(s.keys) {
		g.s = s.keys[end]
	}
	s.gaps = append(s.gaps, gapLock{tx: tx, gap: g})
	out := make([]int64, end-start)
	copy(out, s.keys[start:end])
	return out, nil
}

// Get 点读键 k：存在则只加 mode 记录锁并返回 true；
// 不存在则只对 k 所在的 (p, s) 空隙加锁并返回 false。
func (s *Set) Get(tx TxID, key int64, mode Mode) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return false, err
	}
	if mode != Shared && mode != Exclusive {
		return false, &Error{Kind: ErrInvalidMode, Tx: tx}
	}
	idx := searchInts(s.keys, key)
	if idx < len(s.keys) && s.keys[idx] == key {
		if holder, ok := s.recConflict(tx, key, mode); ok {
			return false, &Error{Kind: ErrLockConflict, Tx: tx, Holder: holder, Key: key}
		}
		s.lockRec(tx, key, mode)
		return true, nil
	}
	// 键不存在：只对现存前驱 p 与后继 s 构成的开区间 (p, s) 加空隙锁。
	g := gap{pInf: idx == 0, sInf: idx == len(s.keys)}
	if idx > 0 {
		g.p = s.keys[idx-1]
	}
	if idx < len(s.keys) {
		g.s = s.keys[idx]
	}
	s.gaps = append(s.gaps, gapLock{tx: tx, gap: g})
	return false, nil
}

// Insert 插入键 k 并令插入者持有 k 的排他记录锁。
func (s *Set) Insert(tx TxID, key int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return err
	}
	idx := searchInts(s.keys, key)
	if idx < len(s.keys) && s.keys[idx] == key {
		return &Error{Kind: ErrKeyExists, Tx: tx, Key: key}
	}
	// k 落在其他事务持有的任一空隙锁区间内则拒绝；自己的锁不与自己冲突。
	var holder TxID
	for _, gl := range s.gaps {
		if gl.tx == tx || !gl.gap.contains(key) {
			continue
		}
		if holder == 0 || gl.tx < holder {
			holder = gl.tx
		}
	}
	if holder != 0 {
		return &Error{Kind: ErrGapOccupied, Tx: tx, Holder: holder, Key: key}
	}
	s.keys = append(s.keys, 0)
	copy(s.keys[idx+1:], s.keys[idx:])
	s.keys[idx] = key
	s.lockRec(tx, key, Exclusive)
	s.inserted[tx] = append(s.inserted[tx], key)
	return nil
}

// Commit 提交事务并释放其全部锁。
func (s *Set) Commit(tx TxID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return err
	}
	s.release(tx)
	s.txs[tx] = txCommitted
	return nil
}

// Abort 中止事务，释放其全部锁并撤销它的全部插入。
func (s *Set) Abort(tx TxID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTx(tx); err != nil {
		return err
	}
	for _, key := range s.inserted[tx] {
		idx := searchInts(s.keys, key)
		if idx < len(s.keys) && s.keys[idx] == key {
			s.keys = append(s.keys[:idx], s.keys[idx+1:]...)
			delete(s.rec, key)
		}
	}
	s.release(tx)
	s.txs[tx] = txAborted
	return nil
}

// searchInts 返回 keys（升序）中第一个不小于 key 的下标。
func searchInts(keys []int64, key int64) int {
	return sort.Search(len(keys), func(i int) bool { return keys[i] >= key })
}

// checkTx 按「不存在、已提交、已中止」的顺序校验事务状态。
func (s *Set) checkTx(tx TxID) error {
	st, ok := s.txs[tx]
	if !ok {
		return &Error{Kind: ErrTxNotFound, Tx: tx}
	}
	switch st {
	case txCommitted:
		return &Error{Kind: ErrTxCommitted, Tx: tx}
	case txAborted:
		return &Error{Kind: ErrTxAborted, Tx: tx}
	}
	return nil
}

// recConflict 判断 tx 以 mode 加 key 的记录锁是否与他人冲突，
// 冲突时返回最小的持锁事务号以保证结果确定。
func (s *Set) recConflict(tx TxID, key int64, mode Mode) (TxID, bool) {
	var holder TxID
	for h, m := range s.rec[key] {
		if h == tx || (m == Shared && mode == Shared) {
			continue
		}
		if holder == 0 || h < holder {
			holder = h
		}
	}
	return holder, holder != 0
}

// lockRec 为 tx 在 key 上登记 mode 记录锁；共享可升级为排他。
func (s *Set) lockRec(tx TxID, key int64, mode Mode) {
	holders := s.rec[key]
	if holders == nil {
		holders = make(map[TxID]Mode)
		s.rec[key] = holders
	}
	if cur, ok := holders[tx]; !ok || (cur == Shared && mode == Exclusive) {
		holders[tx] = mode
	}
}

// release 释放 tx 持有的全部记录锁与空隙锁。
func (s *Set) release(tx TxID) {
	for key, holders := range s.rec {
		delete(holders, tx)
		if len(holders) == 0 {
			delete(s.rec, key)
		}
	}
	kept := s.gaps[:0]
	for _, gl := range s.gaps {
		if gl.tx != tx {
			kept = append(kept, gl)
		}
	}
	s.gaps = kept
	delete(s.inserted, tx)
}
