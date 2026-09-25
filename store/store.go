package store

import (
	"sort"
	"sync"
)

// entry 是单个键的一个历史版本，旧版本通过 prev 串成版本链。
type entry struct {
	key   string
	value []byte
	ver   uint64
	prev  *entry
}

// Store 是支持并发写入与按版本读取的键值存储。
// 写入只向版本链头追加新版本，旧版本由最老活快照决定何时释放。
type Store struct {
	mu      sync.Mutex
	current uint64
	data    map[string]*entry
	active  map[uint64]int // 快照版本水位 -> 重叠快照数
	dirty   map[string]bool
}

func New() *Store {
	return &Store{
		data:   make(map[string]*entry),
		active: make(map[uint64]int),
		dirty:  make(map[string]bool),
	}
}

// Version 返回当前版本水位。
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Put 追加一个新版本；空值合法（len(value)==0 与键不存在可区分）。
func (s *Store) Put(key string, value []byte) {
	s.mu.Lock()
	s.current++
	v := make([]byte, len(value))
	copy(v, value)
	if old := s.data[key]; old != nil {
		s.data[key] = &entry{key: key, value: v, ver: s.current, prev: old}
		s.dirty[key] = true
	} else {
		s.data[key] = &entry{key: key, value: v, ver: s.current}
	}
	s.mu.Unlock()
}

// Begin 登记一个版本水位为 v 的快照，返回后该水位下的旧版本不会被释放。
func (s *Store) Begin(v uint64) {
	s.mu.Lock()
	s.active[v]++
	s.mu.Unlock()
}

// End 关闭一个水位为 v 的快照，并在最老快照推进时惰性回收旧版本。
func (s *Store) End(v uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[v]--
	if s.active[v] <= 0 {
		delete(s.active, v)
	}
	if len(s.active) == 0 {
		for k := range s.dirty {
			s.data[k] = s.data[k].headCopy()
		}
		s.dirty = make(map[string]bool)
		return
	}
	oldest, _ := s.oldestLocked()
	for k := range s.dirty {
		chain := s.data[k]
		pruned := chain.headCopyBelow(oldest)
		if pruned.prev == nil {
			delete(s.dirty, k)
		}
		s.data[k] = pruned
	}
}

func (s *Store) oldestLocked() (uint64, bool) {
	var min uint64
	first := true
	for v := range s.active {
		if first || v < min {
			min, first = v, false
		}
	}
	return min, !first
}

// headCopy 返回链头的独立拷贝（全部快照关闭时只保留当前值）。
func (e *entry) headCopy() *entry {
	v := make([]byte, len(e.value))
	copy(v, e.value)
	return &entry{key: e.key, value: v, ver: e.ver}
}

// headCopyBelow 裁掉“最老快照可见版本”更老的尾部版本。
// 沿链保留链头到 ver <= oldest 的最新条目（含），该条目即最老快照读取到的旧值。
func (e *entry) headCopyBelow(oldest uint64) *entry {
	var cutoff *entry
	for cur := e; cur != nil; cur = cur.prev {
		if cur.ver <= oldest {
			cutoff = cur
			break
		}
	}
	if cutoff == nil {
		return e.headCopy()
	}
	root := &entry{}
	tail := root
	for cur := e; cur != nil; cur = cur.prev {
		node := &entry{key: cur.key, ver: cur.ver, value: append([]byte(nil), cur.value...)}
		tail.prev = node
		tail = node
		if cur == cutoff {
			break
		}
	}
	return root.prev
}

// GetAt 返回版本水位 v 可见的值：沿版本链取第一个 ver <= v 的版本。
func (s *Store) GetAt(key string, v uint64) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for e := s.data[key]; e != nil; e = e.prev {
		if e.ver <= v {
			return append([]byte(nil), e.value...), true
		}
	}
	return nil, false
}

// Keys 返回给定版本水位下存在的全部键（字典序）。
func (s *Store) Keys(v uint64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k, e := range s.data {
		if e.ver <= v {
			keys = append(keys, k)
			continue
		}
		for p := e.prev; p != nil; p = p.prev {
			if p.ver <= v {
				keys = append(keys, k)
				break
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// Retained 返回为水位 v 保留了旧值的键数：链头新于 v、而链中仍存在
// ver <= v 的可见旧值的键。与“被改写的键数”成正比，与总键数无关。
func (s *Store) Retained(v uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k := range s.dirty {
		chain := s.data[k]
		if chain.ver <= v {
			continue
		}
		for e := chain.prev; e != nil; e = e.prev {
			if e.ver <= v {
				n++
				break
			}
		}
	}
	return n
}
