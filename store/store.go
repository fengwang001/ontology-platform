// Package store 用内存 map 模拟目标存储：唯一键约束、幂等写入、
// 可注入的写入失败与操作计数（供线性性断言）。
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrInjected 是故障注入返回的可判定错误。
var ErrInjected = errors.New("store: injected write failure")

// ConflictError 表示同一业务键写入了不同值，违反唯一键约束。
type ConflictError struct{ Key string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("store: conflicting value for key %q", e.Key)
}

// Store 是并发安全的内存键值存储。
type Store struct {
	mu       sync.Mutex
	data     map[string][]byte
	putCalls int // Put 调用序号（供失败注入定位第 k 次）
	failAt   int // 第 failAt 次 Put 注入失败，<=0 表示不注入
	ops      int // Has/Get/Keys/Put 累计次数（供 O(n) 断言）
}

// New 创建空存储。
func New() *Store { return &Store{data: make(map[string][]byte)} }

// FailOnPut 让第 n 次（1 起）Put 调用返回 ErrInjected，触发一次后自动解除。
func (s *Store) FailOnPut(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAt = n
}

// Put 按业务键幂等写入：键不存在则插入；键同值则 no-op（返回 existed=true）；
// 键异值返回 ConflictError。existed 供导入器统计幂等重写条数。
func (s *Store) Put(key string, value []byte) (existed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops++
	s.putCalls++
	if s.failAt > 0 && s.putCalls == s.failAt {
		s.failAt = -1
		return false, ErrInjected
	}
	if old, ok := s.data[key]; ok {
		if string(old) == string(value) {
			return true, nil
		}
		return false, &ConflictError{Key: key}
	}
	s.data[key] = append([]byte(nil), value...)
	return false, nil
}

// Has 报告键是否存在。
func (s *Store) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops++
	_, ok := s.data[key]
	return ok
}

// Keys 返回排序后的全部键（一次操作，供对账线性扫描）。
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops++
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Snapshot 返回存储内容的确定性字节表示，供逐字节比对。
func (s *Store) Snapshot() []byte {
	var b []byte
	for _, k := range s.Keys() {
		s.mu.Lock()
		v := s.data[k]
		s.mu.Unlock()
		b = append(b, fmt.Sprintf("%d %d\n", len(k), len(v))...)
		b = append(b, k...)
		b = append(b, v...)
	}
	return b
}

// Ops 返回累计存储操作次数。
func (s *Store) Ops() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ops
}

// ResetOps 清零操作计数器。
func (s *Store) ResetOps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = 0
}
