// Package store 是支持并发写入与按版本视图读取的键值存储。
package store

import "sync"

// entry 保存当前值与其写入版本；deleted 标记墓碑以区分空值与不存在。
type entry struct {
	value   []byte
	version uint64
	deleted bool
}

// kept 是为某个版本视图保留的旧值；ok=false 表示视图水位前该键不存在。
type kept struct {
	value []byte
	ok    bool
}

// cow 是某个视图私有的写时复制保留表。
type cow map[string]kept

// Store 并发安全。viewReg 按最老活跃视图在前的顺序注册，供 Put 决定保留范围。
type Store struct {
	mu      sync.Mutex
	data    map[string]entry
	version uint64
	views   []*View
}

// View 是某个版本水位上的只读视图，持有该视图私有的写时复制保留表。
type View struct {
	base   uint64
	store  *Store
	kept   cow
	reads  int
	mu     sync.Mutex
	closed bool
}

// New 创建空存储。
func New() *Store {
	return &Store{data: make(map[string]entry)}
}

// Version 返回当前最新版本号。
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// View 在当前版本上创建只读视图并登记，调用方最终必须 Close。
func (s *Store) View() *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	v := &View{base: s.version, store: s, kept: make(cow)}
	s.views = append(s.views, v)
	return v
}

// Put 写入键值。对所有水位早于新版本的活跃视图，保留其可见旧值（每键仅保留一次）。
func (s *Store) Put(key string, value []byte) {
	s.mu.Lock()
	old, existed := s.data[key]
	s.version++
	nv := s.version
	for _, v := range s.views {
		if v.base >= nv {
			continue // 视图创建于本次写入之后，旧值对它从不可见。
		}
		if _, ok := v.kept[key]; ok {
			continue // 已保留该视图水位前的旧值，后续覆盖不再改写。
		}
		if existed && old.version <= v.base {
			v.kept[key] = kept{value: old.value, ok: true}
		} else if !existed {
			v.kept[key] = kept{} // 视图水位前该键不存在。
		}
	}
	s.data[key] = entry{value: append([]byte(nil), value...), version: nv}
	s.mu.Unlock()
}

// Get 读取当前（最新）值；ok=false 表示键不存在，空值与不存在可区分。
func (s *Store) Get(key string) (value []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok || e.deleted {
		return nil, false
	}
	return e.value, true
}

// Keys 返回当前所有未删除键（快照视图下可能含已被全局删除键，见 View.Keys）。
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k, e := range s.data {
		if !e.deleted {
			keys = append(keys, k)
		}
	}
	return keys
}

// Base 返回视图版本水位。
func (v *View) Base() uint64 { return v.base }

// Reads 返回该视图已执行的按版本读取次数。
func (v *View) Reads() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

// Kept 返回为该视图保留的旧值个数（非导出用途的资源计数器）。
func (v *View) Kept() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.kept)
}

// Closed 报告视图是否已关闭。
func (v *View) Closed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closed
}

// Get 按视图版本读取：优先返回写时复制保留的旧值。
func (v *View) Get(key string) (value []byte, ok bool) {
	v.store.mu.Lock()
	v.mu.Lock()
	v.reads++
	if old, isKept := v.kept[key]; isKept {
		v.mu.Unlock()
		v.store.mu.Unlock()
		if !old.ok {
			return nil, false
		}
		return old.value, true
	}
	v.mu.Unlock()
	e, exists := v.store.data[key]
	v.store.mu.Unlock()
	if !exists || e.deleted || e.version > v.base {
		return nil, false
	}
	return e.value, true
}

// Keys 返回视图版本可见的全部键：当前键并上保留表中的键。
func (v *View) Keys() []string {
	v.store.mu.Lock()
	defer v.store.mu.Unlock()
	seen := make(map[string]struct{}, len(v.kept)+len(v.store.data))
	for k, e := range v.store.data {
		if !e.deleted && e.version <= v.base {
			seen[k] = struct{}{}
		}
	}
	for k, old := range v.kept {
		if old.ok {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys
}

// Close 注销视图并释放其全部保留值。
func (v *View) Close() {
	v.store.mu.Lock()
	for i, x := range v.store.views {
		if x == v {
			v.store.views = append(v.store.views[:i], v.store.views[i+1:]...)
			break
		}
	}
	v.mu.Lock()
	v.closed = true
	v.kept = nil
	v.mu.Unlock()
	v.store.mu.Unlock()
}
