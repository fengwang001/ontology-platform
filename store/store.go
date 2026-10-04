// Package store 是按编码字节序（无符号大端逐字节比较）排序的有序存储。
//
// 提供点读、点写、点删与物理区间 [lo, hi) 的升序扫描；扫描只产出区间内的条目。
// Store 本身不做并发控制，由上层（mig）串行化访问。
package store

import (
	"bytes"
	"sort"
)

// Entry 为存储中的一条键值对，Key 为编码后的字节。
type Entry struct {
	Key []byte
	Val int64
}

// Store 按键的字节序升序排列。
type Store struct {
	entries []Entry // 始终按 Key 升序
}

// New 返回空存储。
func New() *Store {
	return &Store{}
}

// search 返回 key 应处的下标；found 表示该下标处恰好是 key。
func (s *Store) search(key []byte) (idx int, found bool) {
	i := sort.Search(len(s.entries), func(i int) bool {
		return bytes.Compare(s.entries[i].Key, key) >= 0
	})
	if i < len(s.entries) && bytes.Equal(s.entries[i].Key, key) {
		return i, true
	}
	return i, false
}

// Get 点读。
func (s *Store) Get(key []byte) (int64, bool) {
	i, found := s.search(key)
	if !found {
		return 0, false
	}
	return s.entries[i].Val, true
}

// Put 点写，覆盖已有值。
func (s *Store) Put(key []byte, v int64) {
	i, found := s.search(key)
	if found {
		s.entries[i].Val = v
		return
	}
	k := append([]byte(nil), key...)
	s.entries = append(s.entries, Entry{})
	copy(s.entries[i+1:], s.entries[i:])
	s.entries[i] = Entry{Key: k, Val: v}
}

// Delete 点删，返回键是否存在。
func (s *Store) Delete(key []byte) bool {
	i, found := s.search(key)
	if !found {
		return false
	}
	copy(s.entries[i:], s.entries[i+1:])
	s.entries = s.entries[:len(s.entries)-1]
	return true
}

// Len 返回条目数。
func (s *Store) Len() int {
	return len(s.entries)
}

// Scan 按字节序升序产出物理区间 [lo, hi) 内的条目；
// lo 为 nil 表示负无穷，hi 为 nil 表示正无穷；limit <= 0 表示不限制条数。
func (s *Store) Scan(lo, hi []byte, limit int) []Entry {
	start := 0
	if lo != nil {
		start = sort.Search(len(s.entries), func(i int) bool {
			return bytes.Compare(s.entries[i].Key, lo) >= 0
		})
	}
	var out []Entry
	for i := start; i < len(s.entries); i++ {
		if hi != nil && bytes.Compare(s.entries[i].Key, hi) >= 0 {
			break
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, s.entries[i])
	}
	return out
}
