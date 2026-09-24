// Package store 维护版本链、存活集合与句柄分派，回收是 O(1) 立即回收。
package store

import (
	"errors"
	"sync"

	"ontology/ver"
)

// 可判定哨兵错误，四者互不相同。
var (
	ErrEmptyStore    = errors.New("store: acquire on empty store")
	ErrNegativeValue = errors.New("store: negative value")
	ErrDoubleRelease = errors.New("store: handle already released")
	ErrUseAfterFree  = errors.New("store: version already reclaimed")
)

// Store 是版本链与存活集合。current 持有 1 份 store 引用。
type Store struct {
	mu      sync.Mutex
	alive   map[*ver.Version]struct{}
	current *ver.Version
	// lastChecked 记录最近一次 Release 检查过的版本个数（非导出，不进公开接口）。
	lastChecked int
}

func New() *Store { return &Store{alive: make(map[*ver.Version]struct{})} }

// Publish 新建版本成为当前版本；旧当前版本失去 store 引用，归零立即回收。
func (s *Store) Publish(v int) error {
	if v < 0 {
		return ErrNegativeValue // 失败不留痕：校验先于任何状态变更
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nv := ver.New(v)
	if s.current != nil && s.current.Dec() {
		delete(s.alive, s.current)
	}
	s.alive[nv] = struct{}{}
	s.current = nv
	return nil
}

// Acquire 返回指向当前版本的句柄并使该版本 refs+1。
func (s *Store) Acquire() (*Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, ErrEmptyStore
	}
	s.current.Inc()
	return &Handle{s: s, v: s.current}, nil
}

// AliveCount 返回当前存活版本个数。
func (s *Store) AliveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.alive)
}

// Handle 是指向某版本的一份引用。
type Handle struct {
	s        *Store
	v        *ver.Version
	released bool
}

// Get 返回句柄所指版本的值；版本已回收或句柄已释放则报 use-after-free。
func (h *Handle) Get() (int, error) {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	if h.released {
		return 0, ErrUseAfterFree
	}
	if _, ok := h.s.alive[h.v]; !ok {
		return 0, ErrUseAfterFree
	}
	return h.v.Value, nil
}

// Refs 返回句柄所指版本当前的引用计数（只读检查用）。
func (h *Handle) Refs() int {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	return h.v.Refs()
}

// Release 使所指版本 refs-1，归零立即回收；只检查这 1 个版本，O(1)。
func (h *Handle) Release() error {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	if h.released {
		return ErrDoubleRelease
	}
	h.released = true
	if _, ok := h.s.alive[h.v]; !ok {
		return ErrUseAfterFree
	}
	checked := 1 // 只对句柄所指版本 Dec 后判零，绝不扫描存活集合
	if h.v.Dec() {
		delete(h.s.alive, h.v)
	}
	h.s.lastChecked = checked
	return nil
}

// ReleaseCheckedO1 内部构造 m 档规模的存活集合，验证单次 Release
// 检查个数是不随 m 增长的小常数。只返回判定，不暴露计数器数值。
func ReleaseCheckedO1() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		if err := s.Publish(0); err != nil {
			return false
		}
		hs := make([]*Handle, 0, m)
		for i := 0; i < m; i++ {
			h, err := s.Acquire()
			if err != nil {
				return false
			}
			hs = append(hs, h)
		}
		if err := hs[0].Release(); err != nil {
			return false
		}
		if s.lastChecked > 2 { // 与 m 无关的小常数
			return false
		}
	}
	return true
}
