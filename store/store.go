// Package store 提供支持并发写入与按版本读取的键值存储。
// 每个键保存一条按版本递增的条目链；存在活跃快照时，被覆盖的旧值
// 作为保留值留在链上，供快照读取；快照关闭后按最老快照水位回收。
package store

import (
	"sort"
	"sync"
	"sync/atomic"
)

type entry struct {
	version uint64
	value   []byte
}

// Store 是版本化键值存储，零值不可用，须用 New 创建。
type Store struct {
	mu        sync.RWMutex
	version   uint64
	data      map[string][]entry
	snapshots map[uint64]int // 活跃快照水位 -> 打开计数
	reads     atomic.Int64   // GetAt 调用次数
	retained  int            // 非最新条目（保留值）个数
}

// New 创建空存储。
func New() *Store {
	return &Store{data: map[string][]entry{}, snapshots: map[uint64]int{}}
}

// Put 写入键值并返回新版本号。键可为空串，值可为空字节串。
func (s *Store) Put(key string, value []byte) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	e := entry{version: s.version, value: append([]byte(nil), value...)}
	old := s.data[key]
	if len(old) == 0 || len(s.snapshots) == 0 {
		// 无快照可见旧值：就地替换，不产生保留值。
		s.data[key] = []entry{e}
		return s.version
	}
	s.data[key] = append(old, e)
	s.retained++
	return s.version
}

// GetAt 读取水位 version 处 key 的可见值（≤ version 的最新条目）。
// 每次调用计一次读取；ok 区分「值为空」与「键不存在」。
func (s *Store) GetAt(key string, version uint64) (value []byte, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.reads.Add(1)
	chain := s.data[key]
	i := sort.Search(len(chain), func(i int) bool { return chain[i].version > version }) - 1
	if i < 0 {
		return nil, false
	}
	return chain[i].value, true
}

// KeysAt 返回水位 version 处可见的全部键，按字典序。
func (s *Store) KeysAt(version uint64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k, chain := range s.data {
		if chain[0].version <= version {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// RegisterSnapshot 注册一个快照，返回其水位（当前版本）。
func (s *Store) RegisterSnapshot() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[s.version]++
	return s.version
}

// ReleaseSnapshot 关闭水位为 version 的一个快照，并按最老活跃水位回收保留值。
func (s *Store) ReleaseSnapshot(version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshots[version] <= 1 {
		delete(s.snapshots, version)
	} else {
		s.snapshots[version]--
	}
	s.gc()
}

// gc 保留：每键最新条目、≤ 最老水位的最新条目、以及 > 最老水位的全部条目。
func (s *Store) gc() {
	oldest, active := uint64(0), false
	for v := range s.snapshots {
		if !active || v < oldest {
			oldest, active = v, true
		}
	}
	s.retained = 0
	for k, chain := range s.data {
		keep := chain
		if !active {
			keep = chain[len(chain)-1:]
		} else if n := sort.Search(len(chain), func(i int) bool { return chain[i].version > oldest }); n > 1 {
			keep = append([]entry{chain[n-1]}, chain[n:]...)
		}
		s.data[k] = keep
		s.retained += len(keep) - 1
	}
}

// Retained 返回当前保留值（非最新条目）个数。
func (s *Store) Retained() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retained
}

// Reads 返回 GetAt 累计调用次数。
func (s *Store) Reads() int {
	return int(s.reads.Load())
}

// Version 返回当前全局版本号。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}
