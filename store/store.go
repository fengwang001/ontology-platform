// Package store 保存对象键的版本记录，支持对象锁（保留截止）、
// 删除标记追加、按版本删除以及 Remove 故障注入。
package store

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// Version 是一个键的一个版本。Ver 为全局版本号（大者新）。
type Version struct {
	Ver    int64
	C      int64
	Marker bool
	R      int64
}

// Plan 描述对单个键的整键原子变更：先追加一个删除标记（可选），
// 再删除给定版本号集合（顺序由调用方语义保证）。
type Plan struct {
	AddMarker bool
	MarkerC   int64
	Remove    []int64
}

// Store 是带并发保护的多版本键值存储。零值不可用，用 New 构造。
type Store struct {
	mu        sync.Mutex
	keys      map[string][]Version
	maxVer    int64
	failAfter int // >0：再经过 failAfter-1 次成功 Remove 后，下一次 Remove 失败；0 表示不注入
}

// ErrRemoveFailed 表示注入的 Remove 失败。
var ErrRemoveFailed = errors.New("store: remove failed")

// New 创建空存储。
func New() *Store {
	return &Store{keys: make(map[string][]Version)}
}

// Load 直接装载一个键的版本（测试用）。
func (s *Store) Load(key []byte, versions []Version) error {
	if len(key) == 0 {
		return errors.New("store: empty key")
	}
	vs := append([]Version(nil), versions...)
	sort.Slice(vs, func(i, j int) bool { return vs[i].Ver < vs[j].Ver })
	var max int64
	seen := make(map[int64]bool, len(vs))
	prevC := int64(-1)
	for _, v := range vs {
		if v.Ver <= 0 {
			return errors.New("store: version number must be positive")
		}
		if seen[v.Ver] {
			return errors.New("store: duplicate version number")
		}
		seen[v.Ver] = true
		if v.C < 0 || v.C > 1_000_000_000_000 {
			return errors.New("store: c out of range [0,10^12]")
		}
		if v.C < prevC {
			return errors.New("store: c must be nondecreasing with version number")
		}
		prevC = v.C
		if v.Marker && v.R != 0 {
			return errors.New("store: delete marker must have r == 0")
		}
		if v.R < 0 {
			return errors.New("store: negative retention")
		}
		if v.Ver > max {
			max = v.Ver
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[string(key)] = vs
	if max > s.maxVer {
		s.maxVer = max
	}
	return nil
}

// Remove 删除指定键的指定版本；失败计数 > 0 时注入一次失败。
func (s *Store) Remove(key []byte, ver int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(string(key), ver)
}

// FailNext 让随后第 n 次 Remove 调用失败（单次注入，0 取消）。
func (s *Store) FailNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 0 {
		n = 0
	}
	s.failAfter = n
}

// Snapshot 返回某键版本号升序的拷贝；ok 为键是否存在。
func (s *Store) Snapshot(key []byte) (versions []Version, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs, ok := s.keys[string(key)]
	if !ok {
		return nil, false
	}
	return append([]Version(nil), vs...), true
}

// KeysFrom 返回 >= cursor 的全部键的升序拷贝；cursor 为空表示从最小键起。
func (s *Store) KeysFrom(cursor []byte) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, 0, len(s.keys))
	for k := range s.keys {
		if len(cursor) > 0 && bytes.Compare([]byte(k), cursor) < 0 {
			continue
		}
		out = append(out, []byte(k))
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

// MaxVer 返回全部已装载版本号的最大值；无版本时返回 0。
func (s *Store) MaxVer() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxVer
}

// TryApply 在锁内对 key 执行 plan：成功提交并返回追加标记的版本号
// （未追加为 0）；任一 Remove 失败则整键回滚并返回错误。
func (s *Store) TryApply(key []byte, plan Plan) (markerVer int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := string(key)
	return s.applyLocked(k, plan)
}

// Modify 在同一把锁内完成"读快照 → 决策 → 提交"：fn 基于 key 当前版本
// 快照与全局最大版本号产出计划与自定义结果；skip 为 true 时不改动存储。
// fn 必须是纯函数（不得回调 Store）。任一 Remove 失败则整键回滚。
func Modify[T any](s *Store, key []byte, fn func(versions []Version, maxVer int64) (plan Plan, ret T, skip bool)) (ret T, processed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := string(key)
	versions := append([]Version(nil), s.keys[k]...)
	plan, ret, skip := fn(versions, s.maxVer)
	if skip {
		return ret, false, nil
	}
	if _, e := s.applyLocked(k, plan); e != nil {
		return ret, true, e
	}
	return ret, true, nil
}

// applyLocked 调用时持有 s.mu。
func (s *Store) applyLocked(key string, plan Plan) (markerVer int64, err error) {
	old := append([]Version(nil), s.keys[key]...)

	if plan.AddMarker {
		markerVer = s.maxVer + 1
		s.keys[key] = append(append([]Version(nil), s.keys[key]...), Version{
			Ver:    markerVer,
			C:      plan.MarkerC,
			Marker: true,
			R:      0,
		})
	}
	for _, ver := range plan.Remove {
		if e := s.removeLocked(key, ver); e != nil {
			if len(old) == 0 {
				delete(s.keys, key)
			} else {
				s.keys[key] = old
			}
			return 0, e
		}
	}
	if len(s.keys[key]) == 0 {
		delete(s.keys, key)
	}
	if markerVer > 0 {
		s.maxVer = markerVer
	}
	return markerVer, nil
}

// removeLocked 调用时持有 s.mu。
func (s *Store) removeLocked(key string, ver int64) error {
	if s.failAfter > 0 {
		s.failAfter--
		if s.failAfter == 0 {
			return ErrRemoveFailed
		}
	}
	vs := s.keys[key]
	for i, v := range vs {
		if v.Ver == ver {
			s.keys[key] = append(vs[:i], vs[i+1:]...)
			break
		}
	}
	return nil
}
