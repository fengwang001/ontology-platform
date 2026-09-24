// Package store 提供双时间记录的写入、更正与逻辑删除。
// 事务时间只追加：更正/删除先把旧记录事务区间截止，再追加新记录。
package store

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/interval"
	"ontology/record"
)

var (
	// ErrOverlap 表示写入的有效区间与该键当前事实冲突。
	ErrOverlap = errors.New("store: valid range conflicts with current fact")
	// ErrNotFound 表示更正/删除的目标记录不存在。
	ErrNotFound = errors.New("store: no matching record")
)

// Store 是进程内存中的双时间存储，记录按键分桶。
type Store struct {
	mu      sync.RWMutex
	now     int64
	byKey   map[string][]record.Record
	touched atomic.Int64
}

// New 返回空存储。
func New() *Store {
	return &Store{byKey: make(map[string][]record.Record)}
}

// Touched 返回最近一次变更操作触及的记录数。
func (s *Store) Touched() int64 { return s.touched.Load() }

// Put 写入新事实，事务区间从当前事务时刻起到 Forever。
// 若与该键任一未截止记录的有效区间重叠，返回 ErrOverlap。
func (s *Store) Put(key string, value int64, valid interval.Interval) (int64, error) {
	if !valid.OK() {
		return 0, interval.ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched.Store(0)
	for _, r := range s.byKey[key] {
		s.touched.Add(1)
		if r.Current() && r.Valid.Overlaps(valid) {
			return 0, ErrOverlap
		}
	}
	return s.appendLocked(key, value, valid), nil
}

// Correct 更正有效区间恰为 valid 的当前记录：
// 旧记录事务区间在当前事务时刻截止，追加一条新记录。
func (s *Store) Correct(key string, value int64, valid interval.Interval) (int64, error) {
	if !valid.OK() {
		return 0, interval.ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched.Store(0)
	recs := s.byKey[key]
	idx := -1
	for i := range recs {
		s.touched.Add(1)
		if recs[i].Current() && recs[i].Valid == valid {
			idx = i
		}
	}
	if idx < 0 {
		return 0, ErrNotFound
	}
	recs[idx].Tx.End = s.now
	s.touched.Add(1)
	return s.appendLocked(key, value, valid), nil
}

// Delete 逻辑删除：覆盖时刻 at 的当前记录，其有效区间在 at 截断。
func (s *Store) Delete(key string, at int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched.Store(0)
	recs := s.byKey[key]
	idx := -1
	for i := range recs {
		s.touched.Add(1)
		if recs[i].Current() && recs[i].Valid.Contains(at) {
			idx = i
		}
	}
	if idx < 0 {
		return 0, ErrNotFound
	}
	old := recs[idx]
	recs[idx].Tx.End = s.now
	s.touched.Add(1)
	if old.Valid.Start < at {
		keep, _ := interval.New(old.Valid.Start, at)
		return s.appendLocked(key, old.Value, keep), nil
	}
	tx := s.now
	s.now++
	return tx, nil
}

// appendLocked 追加一条事务区间 [now, Forever) 的记录并推进事务时钟。
func (s *Store) appendLocked(key string, value int64, valid interval.Interval) int64 {
	tx := s.now
	s.byKey[key] = append(s.byKey[key], record.Record{
		Key:   key,
		Value: value,
		Valid: valid,
		Tx:    interval.Interval{Start: tx, End: interval.Forever},
	})
	s.touched.Add(1)
	s.now++
	return tx
}

// Versions 返回该键版本链的快照副本，供查询端遍历。
func (s *Store) Versions(key string) []record.Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]record.Record, len(s.byKey[key]))
	copy(out, s.byKey[key])
	return out
}

// checkInvariant 非导出自检：同键任意两条记录的矩形互不相交。
func (s *Store) checkInvariant() error {
	for key, recs := range s.byKey {
		for i := range recs {
			for j := i + 1; j < len(recs); j++ {
				if !record.Disjoint(recs[i], recs[j]) {
					return fmt.Errorf("store: key %q has overlapping rectangles", key)
				}
			}
		}
	}
	return nil
}

// CheckInvariant 是 checkInvariant 的导出包装，供包外（demo）调用。
func (s *Store) CheckInvariant() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkInvariant()
}
