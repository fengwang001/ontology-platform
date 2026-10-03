// Package version 实现全桶单调版本号下的每键版本链与当前版本指针。
// 每键是一条双向链表，尾部即当前版本；删除当前版本只需触碰
// 当前节点与其前驱（至多 2 条版本记录），与全桶版本总数无关。
package version

import (
	"sync"

	"ontology/lock"
)

// rec 是一个版本记录；prev/next 组成每键双向版本链，尾部为当前版本。
type rec struct {
	ver    int64
	key    []byte
	size   int64
	marker bool
	lk     lock.L
	prev   *rec
	next   *rec
}

// Store 保存全桶版本。Store 自身不做内部加锁：并发安全由上层
// bucket 用其内嵌的 Mutex 精确串行化；这样 touched 计数的作用域
// 也与一次串行操作严格对齐。
type Store struct {
	sync.Mutex
	byKey    map[string]*rec   // 每个键 -> 链表尾（当前版本）
	byVer    map[int64]*rec    // 版本号 -> 记录
	touchSet map[*rec]struct{} // 自上次 resetTouched 起触碰的不同记录
}

// Rec 是上层读到的版本视图，持有锁状态的可改指针。
type Rec struct {
	Ver    int64
	Key    []byte
	Size   int64
	Marker bool
	Lock   *lock.L
}

func NewStore() *Store {
	return &Store{byKey: map[string]*rec{}, byVer: map[int64]*rec{}, touchSet: map[*rec]struct{}{}}
}

// touch 记录一次对版本记录的触碰；同一记录重复触碰只计一次。
func (s *Store) touch(r *rec) {
	if r != nil {
		s.touchSet[r] = struct{}{}
	}
}

func (s *Store) view(r *rec) Rec {
	return Rec{Ver: r.ver, Key: r.key, Size: r.size, Marker: r.marker, Lock: &r.lk}
}

// Append 追加一个新版本到键链尾部，ver 由上层单调分配。
func (s *Store) Append(key []byte, size int64, marker bool, ver int64) {
	r := &rec{ver: ver, key: key, size: size, marker: marker}
	tail := s.byKey[string(key)]
	if tail != nil {
		r.prev = tail
		tail.next = r
	}
	s.byKey[string(key)] = r
	s.byVer[ver] = r
}

// Current 返回键的当前版本；触碰当前记录 1 条。
func (s *Store) Current(key []byte) (Rec, bool) {
	r := s.byKey[string(key)]
	if r == nil {
		return Rec{}, false
	}
	s.touch(r)
	return s.view(r), true
}

// Find 按版本号定位；触碰 1 条记录（键不匹配视为不存在）。
func (s *Store) Find(key []byte, ver int64) (Rec, bool) {
	r := s.byVer[ver]
	if r == nil || string(r.key) != string(key) {
		return Rec{}, false
	}
	s.touch(r)
	return s.view(r), true
}

// Delete 永久删除指定版本；若删的是当前版本则把当前指针重指到前驱。
// 返回 wasCurrent。删当前版本触碰被删节点与其前驱（至多 2 条），
// 删非当前版本触碰 1 条。
func (s *Store) Delete(key []byte, ver int64) bool {
	r := s.byVer[ver]
	if r == nil || string(r.key) != string(key) {
		return false
	}
	s.touch(r)
	if r.prev != nil {
		r.prev.next = r.next
	}
	if r.next != nil {
		r.next.prev = r.prev
	}
	if s.byKey[string(key)] == r {
		s.touch(r.prev)
		if r.prev != nil {
			s.byKey[string(key)] = r.prev
		} else {
			delete(s.byKey, string(key))
		}
	}
	delete(s.byVer, ver)
	return true
}

// touchedCount 是非导出计数器：一次逻辑操作中触碰的不同版本记录数。
func (s *Store) touchedCount() int { return len(s.touchSet) }

func (s *Store) resetTouched() { s.touchSet = map[*rec]struct{}{} }

func (s *Store) totalVersions() int { return len(s.byVer) }
