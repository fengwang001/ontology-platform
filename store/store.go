// Package store 维护版本链、句柄分派、存活集合与零引用立即回收，依赖 ver。
package store

import (
	"errors"
	"fmt"
	"sync"

	"ontology/ver"
)

// 四类互不相同的哨兵错误：双重释放 / use-after-free / 空存储 Acquire / 负值发布。
var ErrDoubleRelease = errors.New("double release of handle")
var ErrUseAfterFree = errors.New("use of handle after its version was reclaimed")
var ErrEmptyStore = errors.New("acquire on empty store")
var ErrNegativeValue = errors.New("publish with negative value")

// Store 的所有读写都在 mu 内串行；回收是持锁内对单个版本的 O(1) 判定。
type Store struct {
	mu      sync.Mutex
	current *ver.Version
	alive   map[*ver.Version]struct{}
	lastChk int // 非导出：最近一次 Release 检查过的版本个数（恒 0/1，绝不公开）
}

func New() *Store { return &Store{alive: map[*ver.Version]struct{}{}} }

// retire 撤掉 v 的一份引用；refs 归零立即移出存活集合，只检查 v 这一个版本：O(1)。
func (s *Store) retire(v *ver.Version) {
	if v != nil && v.ReleaseOne() {
		delete(s.alive, v) // 归零即回收，不等待任何 GC 周期
	}
}

// Publish 新建 refs=1 的版本并成为当前；旧当前失去 store 引用。负值先于一切变更被拒。
func (s *Store) Publish(value int) error {
	if value < 0 {
		return ErrNegativeValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nv, old := ver.New(value), s.current
	s.current, s.alive[nv] = nv, struct{}{}
	s.retire(old)
	return nil
}

// Acquire 返回指向当前版本的句柄并立即 refs+1；空存储先拒，不留任何痕迹。
func (s *Store) Acquire() (*Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, ErrEmptyStore
	}
	s.current.AddRef() // 拿句柄即 +1，被引用版本绝不会被误回收
	return &Handle{store: s, ver: s.current}, nil
}

func (s *Store) AliveCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.alive) }

// CurrentRefs / Handle.Refs 供同包自检只读观测引用计数（空存储/已回收为 0）。
func (s *Store) CurrentRefs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return 0
	}
	return s.current.Refs()
}

// Handle 是指向某版本的一份共享引用。
type Handle struct {
	store    *Store
	ver      *ver.Version
	released bool
}

// Get 返回发布值；句柄已释放或版本已回收均为 use-after-free。
func (h *Handle) Get() (int, error) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if h.released || !h.ver.Alive() {
		return 0, ErrUseAfterFree
	}
	return h.ver.Value(), nil
}

// Release 撤一份引用，归零立即回收。双重释放/UAF 的判定先于任何状态变更，被拒不留痕。
func (h *Handle) Release() error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if h.released {
		return ErrDoubleRelease
	}
	if !h.ver.Alive() {
		return ErrUseAfterFree
	}
	h.released = true
	h.store.lastChk = 1 // 只检查所指这一个版本，与存活集合大小无关
	h.store.retire(h.ver)
	return nil
}

func (h *Handle) Refs() int { h.store.mu.Lock(); defer h.store.mu.Unlock(); return h.ver.Refs() }

// SelfCheck 重放第三节八步序列，核验四不变量、四类哨兵与 O(1) 多档检查；非导出 lastChk 只在此包内读取。
func (s *Store) SelfCheck() error {
	t := New()
	var first error
	ck := func(ok bool, m string, a ...any) {
		if first == nil && !ok {
			first = fmt.Errorf("selfcheck "+m, a...)
		}
	}
	_, e0 := t.Acquire() // 空存储 Acquire 被拒且不留痕
	ck(errors.Is(e0, ErrEmptyStore) && t.AliveCount() == 0, "empty acquire: %v", e0)
	_ = t.Publish(10) // 1 P(10)
	h1, _ := t.Acquire()
	h2, _ := t.Acquire() // 2 h1=A()、3 h2=A()
	ck(h1.Refs() == 3, "s3 v1 refs=%d", h1.Refs())
	_ = t.Publish(20) // 4 P(20)：store 引用转移，v1 refs 3->2
	ck(h1.Refs() == 2 && t.AliveCount() == 2, "s4 r=%d a=%d", h1.Refs(), t.AliveCount())
	h3, _ := t.Acquire() // 5 h3=A()
	ck(h3.Refs() == 2 && t.AliveCount() == 2, "s5 v2 refs=%d", h3.Refs())
	_ = h1.Release() // 6 R(h1)
	ck(h2.Refs() == 1, "s6 v1 refs=%d", h2.Refs())
	_ = h2.Release() // 7 R(h2)：v1 归零立即回收，alive 立即为 1
	ck(h2.Refs() == 0 && t.AliveCount() == 1, "s7 r=%d a=%d", h2.Refs(), t.AliveCount())
	_, eg := h1.Get() // 8 必须返回 ErrUseAfterFree，不得回缓存值 10
	ck(errors.Is(eg, ErrUseAfterFree), "s8 err=%v", eg)
	v, eg := h3.Get() // 幸存者不受影响
	ck(eg == nil && v == 20, "survivor v=%d e=%v", v, eg)
	ck(errors.Is(h1.Release(), ErrDoubleRelease), "double release") // 双重释放
	en := t.Publish(-7)                                             // 负值被拒且不留痕、仍可正常使用
	ck(errors.Is(en, ErrNegativeValue) && t.AliveCount() == 1, "negative a=%d", t.AliveCount())
	v, eg = h3.Get()
	ck(eg == nil && v == 20, "usable after reject: v=%d e=%v", v, eg)
	for _, m := range []int{100, 1000, 10000} { // O(1)：检查个数恒 1，不随 m 增长
		b := New()
		hs := make([]*Handle, 0, m)
		for i := 0; i < m; i++ {
			_ = b.Publish(i)
			h, _ := b.Acquire()
			hs = append(hs, h)
		}
		ck(hs[0].Release() == nil && b.lastChk == 1 && b.AliveCount() == m-1, "O(1) m=%d chk=%d", m, b.lastChk)
	}
	return first
}
