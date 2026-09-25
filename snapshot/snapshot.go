// Package snapshot 提供只读一致性快照：版本水位 + 写时复制保留旧值。
package snapshot

import (
	"sort"
	"sync"

	"ontology/store"
)

// Snapshot 是某一版本水位上的只读视图。
type Snapshot struct {
	st  *store.Store
	ver uint64

	mu       sync.Mutex
	kept     map[string]store.Entry
	keptN    int
	closed   bool
	active   int
	readKeys int
	done     *sync.Cond
	keys     []string
}

// Open 在 store 当前版本上打开快照并登记保留回调。
func Open(st *store.Store) *Snapshot {
	s := &Snapshot{st: st, ver: st.Version(), kept: make(map[string]store.Entry)}
	s.done = sync.NewCond(&s.mu)
	st.Register(s)
	s.keys = st.Keys()
	sort.Strings(s.keys)
	return s
}

// Watermark 实现 store.Preserver，返回快照版本水位。
func (s *Snapshot) Watermark() uint64 { return s.ver }

// Preserve 实现 store.Preserver：仅保留一次（首次 > 水位的覆盖前旧值）。
func (s *Snapshot) Preserve(key string, old store.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if _, ok := s.kept[key]; ok {
		return
	}
	s.kept[key] = old
	s.keptN++
}

// Begin 开始一次导出读取；快照关闭期间不允许新导出。
func (s *Snapshot) Begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.active++
	return true
}

// Done 结束一次导出读取。
func (s *Snapshot) Done() {
	s.mu.Lock()
	s.active--
	if s.active == 0 {
		s.done.Broadcast()
	}
	s.mu.Unlock()
}

// Live 报告快照是否仍可用（未关闭）。
func (s *Snapshot) Live() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// Version 返回快照水位版本。
func (s *Snapshot) Version() uint64 { return s.ver }

// PreservedCount 返回当前为该快照保留的旧值个数（非导出计数器）。
func (s *Snapshot) PreservedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keptN
}

// ReadCount 返回本快照上 Snapshot.Get 的总读取次数。
func (s *Snapshot) ReadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readKeys
}

// Get 返回快照时刻键的可见状态；每个键调用方保证只读一次。
func (s *Snapshot) Get(key string) store.Entry {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return store.Entry{Absent: true}
	}
	e, ok := s.kept[key]
	s.readKeys++
	s.mu.Unlock()
	if ok {
		return e
	}
	cur := s.st.Current(key)
	if cur.Ver <= s.ver {
		return cur
	}
	return store.Entry{Absent: true}
}

// Keys 返回快照时刻固化的键集合（已排序副本）。
func (s *Snapshot) Keys() []string {
	out := make([]string, len(s.keys))
	copy(out, s.keys)
	return out
}

// Close 等待在途导出结束，随后释放全部保留值并使快照过期。
func (s *Snapshot) Close() {
	s.mu.Lock()
	s.closed = true
	for s.active > 0 {
		s.done.Wait()
	}
	s.kept = nil
	s.keptN = 0
	s.mu.Unlock()

	s.st.Unregister(s)
}
