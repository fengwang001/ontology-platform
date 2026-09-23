// Package store 是目标存储的内存模拟：业务键唯一、按批次隔离、
// 区分在途与已提交状态，并支持写入失败注入。
package store

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// EntryState 是记录的可见性状态。
type EntryState int

const (
	// Inflight 表示已写入但所属批次尚未提交。
	Inflight EntryState = iota
	// Committed 表示所属批次已提交。
	Committed
)

type entry struct {
	batch string
	value []byte
	state EntryState
}

// Store 是并发安全的内存存储。
type Store struct {
	mu        sync.Mutex
	data      map[string]entry
	batches   map[string]bool
	committed map[string]bool
	failAt    map[int]bool
}

// New 创建空存储。
func New() *Store {
	return &Store{
		data:      map[string]entry{},
		batches:   map[string]bool{},
		committed: map[string]bool{},
		failAt:    map[int]bool{},
	}
}

var (
	// ErrConflict 表示业务键已被其他批次占用。
	ErrConflict = errors.New("business key conflict")
	// ErrValueMismatch 表示同批次同键写入了不同值。
	ErrValueMismatch = errors.New("value mismatch for idempotent key")
	// ErrWriteFailed 为注入的写入失败。
	ErrWriteFailed = errors.New("injected write failure")
)

// FailAt 令全局第 idx 次新写入失败（幂等重放不计入）。
func (s *Store) FailAt(idx int) { s.failAt[idx] = true }

// MarkBatch 登记一个批次（即使零记录也存在，可被提交）。
func (s *Store) MarkBatch(batchID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches[batchID] = true
}

// Write 按 (批次,业务键) 幂等写入。
func (s *Store) Write(batchID, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.data[key]; ok {
		if cur.batch != batchID {
			return ErrConflict
		}
		if !bytes.Equal(cur.value, value) {
			return ErrValueMismatch
		}
		return nil // 幂等命中
	}
	idx := len(s.data)
	if s.failAt[idx] {
		delete(s.failAt, idx)
		return ErrWriteFailed
	}
	s.data[key] = entry{batch: batchID, value: append([]byte(nil), value...), state: Inflight}
	s.batches[batchID] = true
	return nil
}

// Has 判断键是否存在。
func (s *Store) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[key]
	return ok
}

// BatchKeys 返回某批次全部键（未排序）。
func (s *Store) BatchKeys(batchID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0)
	for k, e := range s.data {
		if e.batch == batchID {
			keys = append(keys, k)
		}
	}
	return keys
}

// BatchState 返回批次提交状态与是否存在。
func (s *Store) BatchState(batchID string) (committed, exists bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.data {
		if e.batch == batchID {
			exists = true
			break
		}
	}
	exists = exists || s.batches[batchID]
	return s.committed[batchID], exists
}

// Commit 将某批次全部在途记录提升为已提交。
func (s *Store) Commit(batchID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exists := s.batches[batchID]
	for k, e := range s.data {
		if e.batch == batchID {
			e.state = Committed
			s.data[k] = e
			exists = true
		}
	}
	if !exists {
		return false
	}
	s.committed[batchID] = true
	return true
}

// BatchSnapshot 生成确定性文本快照，用于逐字节比对。
func (s *Store) BatchSnapshot(batchID string) []byte {
	keys := s.BatchKeys(batchID)
	sort.Strings(keys)
	var buf bytes.Buffer
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		e := s.data[k]
		state := byte('I')
		if e.state == Committed {
			state = byte('C')
		}
		buf.WriteByte(state)
		buf.WriteByte('\t')
		buf.WriteString(k)
		buf.WriteByte('\t')
		buf.Write(e.value)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
}
