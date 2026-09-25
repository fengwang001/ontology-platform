package store

import (
	"sort"
	"sync"
)

type entry struct {
	ver     uint64
	present bool
	value   []byte
}

// Store 是支持并发写入与按版本读取的键值存储。
type Store struct {
	mu        sync.Mutex
	data      map[string]entry
	hist      map[string][]entry
	versions  map[uint64]struct{}
	ver       uint64
	retained  int
}

func New() *Store {
	return &Store{
		data:     make(map[string]entry),
		hist:     make(map[string][]entry),
		versions: make(map[uint64]struct{}),
	}
}

// Take 返回当前版本水位与当前全部键的快照视图。
func (s *Store) Take() (uint64, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return s.ver, keys
}

// Register 登记一个活跃快照水位。
func (s *Store) Register(ver uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[ver] = struct{}{}
}

// Put 写入键值；若存在活跃快照且当前项对最老快照可见，则旧值进入历史链。
func (s *Store) Put(key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ver++
	old, ok := s.data[key]
	if ok && len(s.versions) > 0 && old.ver <= s.oldestLocked() {
		s.hist[key] = append(s.hist[key], old)
		s.retained++
	}
	s.data[key] = entry{ver: s.ver, present: true, value: append([]byte(nil), value...)}
}

// Delete 写入墓碑，语义同 Put，保留被覆盖的旧值。
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ver++
	old, ok := s.data[key]
	if ok && len(s.versions) > 0 && old.ver <= s.oldestLocked() {
		s.hist[key] = append(s.hist[key], old)
		s.retained++
}
	s.data[key] = entry{ver: s.ver, present: false}
}

// ReadAt 返回版本 at 时键的可见状态。
func (s *Store) ReadAt(key string, at uint64) (value []byte, present, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.hist[key] {
		if e.ver <= at {
			return e.value, e.present, true
		}
	}
	cur, exists := s.data[key]
	if exists && cur.ver <= at {
		return cur.value, cur.present, true
	}
	return nil, false, false
}

// Retained 返回为活跃快照保留的旧值条数。
func (s *Store) Retained() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retained
}

// Unregister 注销快照，并按新的最老水位释放不再需要的旧值。
func (s *Store) Unregister(ver uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.versions, ver)
	m, has := s.oldestLocked(), len(s.versions) > 0
	for k, h := range s.hist {
		if len(h) == 0 {
			continue
		}
		keep := 0
		if has {
			for keep < len(h) && h[keep].ver >= m {
				keep++
			}
		}
		s.retained -= len(h) - keep
		if keep == 0 {
			delete(s.hist, k)
		} else {
			s.hist[k] = append([]entry(nil), h[:keep]...)
		}
	}
}

func (s *Store) oldestLocked() uint64 {
	var m uint64
	first := true
	for v := range s.versions {
		if first || v < m {
			m, first = v, false
		}
	}
	return m
}
