// Package store 是支持按版本读取的并发键值存储。
package store

import (
	"sort"
	"sync"
)

// Cell 是某时刻某个键的可见状态。Exist 为假表示键不存在，
// 与 Value 为空字节串的情形严格区分。
type Cell struct {
	Exist bool
	Value []byte
}

// Record 是一个键在某次写入后的状态及写入版本。
type Record struct {
	Key   string
	Ver   int64
	Exist bool
	Value []byte
}

// Store 只保留每个键的最新版本；历史版本由上层快照以写时复制保留。
type Store struct {
	mu      sync.RWMutex
	nextVer int64
	data    map[string]Record
}

// New 创建空存储。
func New() *Store {
	return &Store{nextVer: 1, data: make(map[string]Record)}
}

// Put 写入一个键，返回该写入分配的单调版本号。
func (s *Store) Put(key string, value []byte) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.nextVer
	s.nextVer++
	s.data[key] = Record{Key: key, Ver: v, Exist: true, Value: clone(value)}
	return v
}

// Current 返回某键的最新记录；键从未写入时 Exist 为假、Ver 为 0。
func (s *Store) Current(key string) Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data[key]
	if !ok {
		return Record{Key: key}
	}
	r.Value = clone(r.Value)
	return r
}

// ReadAt 读取版本 at 可见的单元：取版本 <= at 的最新写入。
func (s *Store) ReadAt(key string, at int64) Cell {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data[key]
	if !ok || r.Ver > at {
		return Cell{}
	}
	return Cell{Exist: true, Value: clone(r.Value)}
}

// KeysAt 返回版本 at 时可见的全部键，按 orders 给出的顺序排列。
func (s *Store) KeysAt(at int64, orders int) []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.data))
	for k, r := range s.data {
		if r.Ver <= at {
			keys = append(keys, k)
		}
	}
	s.mu.RUnlock()
	switch orders {
	case -1:
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	default:
		sort.Strings(keys)
	}
	return keys
}

// Version 返回下一个将被分配的版本号（即当前水位之后）。
func (s *Store) Version() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextVer
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
