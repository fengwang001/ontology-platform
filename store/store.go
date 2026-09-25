// Package store 提供支持并发写入与按版本读取的 MVCC 键值存储。
package store

import (
	"sort"
	"sync"
	"sync/atomic"
)

type entry struct {
	ver uint64
	val []byte
}

// Store 为每个键保留一条版本链；被取代的旧值在仍有快照钉住旧版本时不释放。
type Store struct {
	mu       sync.RWMutex
	ver      uint64
	data     map[string][]entry
	pins     map[uint64]int
	retained int64
	reads    atomic.Int64
}

func New() *Store {
	return &Store{data: make(map[string][]entry), pins: make(map[uint64]int)}
}

// Put 追加一个新版本并返回该版本号；空键、空值均合法。
func (s *Store) Put(key string, val []byte) uint64 {
	cp := append([]byte(nil), val...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ver++
	chain := s.data[key]
	if len(chain) > 0 {
		s.retained++
	}
	s.data[key] = append(chain, entry{s.ver, cp})
	return s.ver
}

// Get 返回最新值；ok 为 false 表示键不存在（与空值可区分）。
func (s *Store) Get(key string) (val []byte, ok bool) {
	s.reads.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain := s.data[key]
	if len(chain) == 0 {
		return nil, false
	}
	return chain[len(chain)-1].val, true
}

// GetAt 返回版本 ver 可见的值（链上最后一个 ver' <= ver 的 entry）。
func (s *Store) GetAt(key string, ver uint64) (val []byte, ok bool) {
	s.reads.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain := s.data[key]
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].ver <= ver {
			return chain[i].val, true
		}
	}
	return nil, false
}

// Version 返回当前最新版本号。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ver
}

// KeysAt 返回版本 ver 时已存在的键（字典序）。
func (s *Store) KeysAt(ver uint64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k, chain := range s.data {
		if len(chain) > 0 && chain[0].ver <= ver {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Pin 钉住一个版本，返回解除函数；解除时按最老被钉版本裁剪旧值。
func (s *Store) Pin(ver uint64) func() {
	s.mu.Lock()
	s.pins[ver]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.pins[ver]--
			if s.pins[ver] == 0 {
				delete(s.pins, ver)
			}
			s.gcLocked()
		})
	}
}

// gcLocked 丢弃所有严格旧于 floor(最老被钉版本) 的 entry；无钉时只留最新值。
func (s *Store) gcLocked() {
	oldest, pinned := uint64(0), false
	for v := range s.pins {
		if !pinned || v < oldest {
			oldest, pinned = v, true
		}
	}
	for k, chain := range s.data {
		keep := len(chain) - 1
		if pinned {
			keep = 0
			for i, e := range chain {
				if e.ver <= oldest {
					keep = i
				}
			}
		}
		if keep > 0 {
			s.retained -= int64(keep)
			s.data[k] = append([]entry(nil), chain[keep:]...)
		}
	}
}

// Retained 返回当前为快照保留的旧值个数。
func (s *Store) Retained() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retained
}

// Reads 返回 Get/GetAt 累计调用次数。
func (s *Store) Reads() int64 { return s.reads.Load() }

// ResetReads 清零读取计数器。
func (s *Store) ResetReads() { s.reads.Store(0) }
