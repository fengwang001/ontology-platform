// Package store 提供双时间记录的写入、更正与逻辑删除。
// 事务时间只追加：更正/删除通过截止旧记录事务区间并追加新记录实现。
package store

import (
	"errors"
	"fmt"
	"sync"

	"ontology/interval"
	"ontology/record"
)

// ErrOverlap 表示写入与当前可见的同键记录有效区间冲突。
var ErrOverlap = errors.New("store: valid interval overlaps existing record")

// ErrNotFound 表示更正/删除的目标不存在。
var ErrNotFound = errors.New("store: no live record found")

// Store 按键分桶保存版本链，内部时钟保证事务时刻严格递增。
type Store struct {
	mu      sync.RWMutex
	chain   map[string][]record.R
	clock   int64
	touched int // 最近一次更正触及的记录数
}

// New 创建空 Store。
func New() *Store {
	return &Store{chain: make(map[string][]record.R)}
}

// Write 写入新记录，返回其事务时刻。
func (s *Store) Write(key string, value int64, valid interval.I) (int64, error) {
	if valid.Empty() {
		return 0, interval.ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.clock + 1
	for _, r := range s.chain[key] {
		if r.Valid.Overlaps(valid) && r.Tx.Contains(tx) {
			return 0, ErrOverlap
		}
	}
	s.chain[key] = append(s.chain[key], record.New(key, value, valid, tx))
	s.clock = tx
	return tx, nil
}

// Correct 更正：截止与 valid 重叠的现存记录的事务区间，追加新记录。
func (s *Store) Correct(key string, value int64, valid interval.I) (int64, error) {
	if valid.Empty() {
		return 0, interval.ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.clock + 1
	recs := s.chain[key]
	closed := 0
	for i := range recs {
		if recs[i].Valid.Overlaps(valid) && recs[i].Tx.Contains(tx) {
			recs[i].Tx.End = tx
			closed++
		}
	}
	if closed == 0 {
		return 0, ErrNotFound
	}
	s.touched = len(recs) + closed + 1 // 扫描 + 截止 + 追加
	s.chain[key] = append(recs, record.New(key, value, valid, tx))
	s.clock = tx
	return tx, nil
}

// Delete 逻辑删除：把键当前记录的有效区间截断到 validEnd。
func (s *Store) Delete(key string, validEnd int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.clock + 1
	recs := s.chain[key]
	for i := range recs {
		if recs[i].Tx.Contains(tx) && recs[i].Valid.Contains(validEnd) {
			truncated, err := interval.New(recs[i].Valid.Start, validEnd)
			if err != nil {
				return 0, err
			}
			recs[i].Tx.End = tx
			s.chain[key] = append(recs, record.New(key, recs[i].Value, truncated, tx))
			s.clock = tx
			return tx, nil
		}
	}
	return 0, ErrNotFound
}

// Snapshot 返回键的版本链副本，供查询侧只读使用。
func (s *Store) Snapshot(key string) []record.R {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]record.R(nil), s.chain[key]...)
}

// Touched 返回最近一次更正触及的记录数。
func (s *Store) Touched() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.touched
}

// CheckDisjoint 自检：同键任意两条记录的矩形互不相交。
func (s *Store) CheckDisjoint() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkDisjoint()
}

func (s *Store) checkDisjoint() error {
	for key, recs := range s.chain {
		for i := 0; i < len(recs); i++ {
			for j := i + 1; j < len(recs); j++ {
				if !record.RectDisjoint(recs[i], recs[j]) {
					return fmt.Errorf("store: key %q records %d and %d intersect", key, i, j)
				}
			}
		}
	}
	return nil
}
