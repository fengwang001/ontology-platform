// Package store 用内存 map 模拟目标存储：唯一键约束、幂等写、可注入写失败。
package store

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrInjected 是故障注入产生的可判定写失败。
var ErrInjected = errors.New("store: injected write failure")

// ConflictError 表示唯一键冲突：同键不同值。
type ConflictError struct{ Key string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("store: key %q already exists with different value", e.Key)
}

// Store 是并发安全的内存 KV 存储。
type Store struct {
	mu     sync.Mutex
	data   map[string][]byte
	failAt int // 第 failAt 次 Write 调用失败一次；0 关闭注入
	calls  int
	reads  int
}

func New() *Store { return &Store{data: map[string][]byte{}} }

// FailAt 注入一次性故障：第 k 次 Write 调用返回 ErrInjected。
func (s *Store) FailAt(k int) { s.mu.Lock(); s.failAt = k; s.mu.Unlock() }

// Write 按业务键幂等写入：键不存在则插入；键存在且值相同则为无副作用的
// 重放（existed=true）；键存在而值不同报唯一键冲突。
func (s *Store) Write(key string, val []byte) (existed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == s.failAt {
		s.failAt = 0
		return false, ErrInjected
	}
	if old, ok := s.data[key]; ok {
		if bytes.Equal(old, val) {
			return true, nil
		}
		return false, &ConflictError{Key: key}
	}
	s.data[key] = append([]byte(nil), val...)
	return false, nil
}

// Has 报告键是否存在，计入读放大统计。
func (s *Store) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	_, ok := s.data[key]
	return ok
}

// Keys 返回排序后的全部键，按条数计入读放大统计。
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := make([]string, 0, len(s.data))
	for k := range s.data {
		ks = append(ks, k)
	}
	s.reads += len(ks)
	sort.Strings(ks)
	return ks
}

// Reads 返回累计存储访问次数（Has + Keys 按条计）。
func (s *Store) Reads() int { s.mu.Lock(); defer s.mu.Unlock(); return s.reads }

// Len 返回存储中的记录数。
func (s *Store) Len() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.data) }

// Snapshot 把内容序列化为确定性字节流，用于逐字节比对。
func (s *Store) Snapshot() []byte {
	var b bytes.Buffer
	for _, k := range s.Keys() {
		s.mu.Lock()
		b.WriteString(k)
		b.WriteByte(0)
		b.Write(s.data[k])
		s.mu.Unlock()
		b.WriteByte('\n')
	}
	return b.Bytes()
}
