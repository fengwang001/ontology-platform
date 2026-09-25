// Package store 实现热/冷两层归属、值保留与 Put/Get/Value 语义。依赖 lru。
package store

import (
	"errors"
	"sort"
	"sync"

	"ontology/lru"
)

var (
	ErrBadCap   = errors.New("store: hotCap/maxCold 必须为正")
	ErrEmptyKey = errors.New("store: 空键")
	ErrNotFound = errors.New("store: 键不存在")
	ErrColdFull = errors.New("store: 冷层已满，换出被拒绝")
)

// Store 是热冷两层键值存储。值在 val 中始终完整保留，换出不丢值。
type Store struct {
	mu      sync.Mutex
	hotCap  int
	maxCold int
	hot     *lru.LRU
	cold    map[string]bool
	val     map[string]int64
	clock   int64 // 逻辑时钟，从 1 开始，每次成功 Put/Get 自增
}

func New(hotCap, maxCold int) (*Store, error) {
	if hotCap <= 0 || maxCold <= 0 {
		return nil, ErrBadCap
	}
	return &Store{
		hotCap: hotCap, maxCold: maxCold,
		hot: lru.New(), cold: make(map[string]bool),
		val: make(map[string]int64), clock: 1,
	}, nil
}

// touch 把键记为当前时刻被访问并推进时钟。调用方须持锁且保证键在热层。
func (s *Store) touch(key string) {
	s.hot.Touch(key, s.clock)
	s.clock++
}

// evictIfNeeded 热层超限则把 LRU 键换出到冷层（值保留在 val）。
// 调用方须持锁，并已确认冷层容纳得下。
func (s *Store) evictIfNeeded() {
	if s.hot.Len() <= s.hotCap {
		return
	}
	victim, _ := s.hot.LruKey()
	s.hot.Remove(victim)
	s.cold[victim] = true
}

// needEvict 报告「键不在热层、将被加入热层」时是否会触发换出，
// 以及触发时冷层是否会超限（此时整个操作必须前置失败）。
func (s *Store) needEvict(key string) (evict bool, coldFull bool) {
	if s.hot.Has(key) || s.hot.Len()+1 <= s.hotCap {
		return false, false
	}
	coldAfter := len(s.cold)
	if s.cold[key] {
		coldAfter-- // 从冷层换入先进来，冷层先减一
	}
	return true, coldAfter+1 > s.maxCold
}

func (s *Store) Put(key string, v int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, full := s.needEvict(key); full {
		return ErrColdFull // 前置失败：两层归属、值、访问时间均不变
	}
	s.val[key] = v
	if s.hot.Has(key) {
		s.touch(key)
	} else {
		delete(s.cold, key) // 冷层换入（不存在时 delete 无副作用）
		s.hot.Add(key, s.clock)
		s.clock++
	}
	s.evictIfNeeded()
	return nil
}

func (s *Store) Get(key string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hot.Has(key) {
		s.touch(key)
		return s.val[key], nil
	}
	if !s.cold[key] {
		return 0, ErrNotFound
	}
	if _, full := s.needEvict(key); full {
		return 0, ErrColdFull
	}
	delete(s.cold, key)
	s.hot.Add(key, s.clock)
	s.clock++
	s.evictIfNeeded()
	return s.val[key], nil
}

// Value 只读探查：不推进时钟、不改变任何归属。
func (s *Store) Value(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.val[key]
	return v, ok
}

// HotKeys 返回热层键，MRU→LRU 顺序。
func (s *Store) HotKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hot.Keys()
}

// ColdKeys 返回冷层键，字典序升序。
func (s *Store) ColdKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.cold))
	for k := range s.cold {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
