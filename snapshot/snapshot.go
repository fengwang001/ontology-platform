// Package snapshot 维护"当前版本"原子指针：读路径零锁、只 Load 一次；
// 写路径在互斥锁内克隆-修改-原子发布（copy-on-write）。
package snapshot

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/ver"
)

// ErrTooManyKeys 表示 Update 会使不同 key 的总数超过 maxKeys。
var ErrTooManyKeys = errors.New("snapshot: too many keys")

// Store 是写时复制的键值存储。
type Store struct {
	mu      sync.Mutex // 仅写者使用，读路径绝不触碰
	cur     atomic.Pointer[ver.Version]
	maxKeys int
	// accessed 记录最近一次读操作访问的 map 条目个数。
	// 非导出字段，不出现在任何公开接口；仅同包白盒测试可观测。
	accessed atomic.Int64
}

// New 构造空存储，当前版本为 id=0 的空表。
func New(maxKeys int) *Store {
	s := &Store{maxKeys: maxKeys}
	s.cur.Store(ver.New())
	return s
}

// Update 写锁内：克隆当前版本 → 改 → 原子 Store 发布新版本。
// 新增 key 使总数超 maxKeys 时整体失败，状态不变。
func (s *Store) Update(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.cur.Load()
	if _, ok := cur.M[k]; !ok && cur.Len()+1 > s.maxKeys {
		return ErrTooManyKeys
	}
	m := cur.Clone()
	m[k] = v
	s.cur.Store(&ver.Version{Id: cur.Id + 1, M: m})
	return nil
}

// Read 原子 Load 一次指针，返回该版本的 k；不存在则 ok=false。
func (s *Store) Read(k string) (string, bool) {
	v := s.cur.Load()
	s.accessed.Store(1)
	return v.Get(k)
}

// ReadKeys 恰好原子 Load 一次指针，所有 key 取自同一个版本。
func (s *Store) ReadKeys(ks []string) map[string]string {
	v := s.cur.Load()
	out := make(map[string]string, len(ks))
	var n int64
	for _, k := range ks {
		if val, ok := v.Get(k); ok {
			out[k] = val
		}
		n++
	}
	s.accessed.Store(n)
	return out
}

// ID 返回当前版本 id（只读观测，用于核验"失败不留痕"）。
func (s *Store) ID() int {
	return s.cur.Load().Id
}

// Handle 是不可变版本的句柄：永远读到 Snapshot 那一刻的版本。
type Handle struct {
	v *ver.Version
}

// Snapshot 原子 Load 一次指针，返回该版本的句柄。
func (s *Store) Snapshot() *Handle {
	return &Handle{v: s.cur.Load()}
}

// Read 从句柄绑定的不可变版本读 k，不受后续任何 Update 影响。
func (h *Handle) Read(k string) (string, bool) {
	return h.v.Get(k)
}
