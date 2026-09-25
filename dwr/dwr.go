// Package dwr 实现双写副本与对账。依赖 rec。
package dwr

import (
	"errors"
	"sync"

	"ontology/rec"
)

// ErrSide 表示 side 不是 0(A) 或 1(B)。
var ErrSide = errors.New("dwr: side must be 0(A) or 1(B)")

// Store 是两个副本 A、B 加脏集合。checked 记录最近一次 Reconcile 检查过的键个数，
// 非导出，不出现在任何公开接口。
type Store struct {
	mu      sync.RWMutex
	a, b    map[string]rec.Record
	dirty   map[string]struct{} // 分歧键集合：Reconcile 只遍历它，不做整表扫描
	maxVer  int64
	checked int
}

func New() *Store {
	return &Store{a: map[string]rec.Record{}, b: map[string]rec.Record{}, dirty: map[string]struct{}{}}
}

// validate 在任何状态修改前做完整校验，保证失败不留痕。
func (s *Store) validate(key string, ver int64) error {
	if key == "" {
		return rec.ErrKey
	}
	return rec.CheckVer(ver, s.maxVer)
}

// writeBoth 双写；writeOne 只写一侧并把键记入脏集合。调用前必须先通过校验。
func (s *Store) writeBoth(r rec.Record, key string) { s.a[key], s.b[key] = r, r; delete(s.dirty, key) }
func (s *Store) writeOne(side int, r rec.Record, key string) {
	if side == 0 {
		s.a[key] = r
	} else {
		s.b[key] = r
	}
	s.dirty[key] = struct{}{}
}

func (s *Store) put(key, val string, ver int64, one bool, side int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(key, ver); err != nil {
		return err
	}
	r, err := rec.Live(key, val, ver)
	if err != nil {
		return err
	}
	if one {
		s.writeOne(side, r, key)
	} else {
		s.writeBoth(r, key)
	}
	s.maxVer = ver // 全部校验通过、状态写完后才推进全局版本
	return nil
}

func (s *Store) del(key string, ver int64, one bool, side int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(key, ver); err != nil {
		return err
	}
	r, err := rec.Tomb(key, ver)
	if err != nil {
		return err
	}
	if one {
		s.writeOne(side, r, key)
	} else {
		s.writeBoth(r, key)
	}
	s.maxVer = ver
	return nil
}

func (s *Store) Put(k, v string, ver int64) error { return s.put(k, v, ver, false, 0) }
func (s *Store) Del(k string, ver int64) error    { return s.del(k, ver, false, 0) }
func (s *Store) PutOne(side int, k, v string, ver int64) error {
	if side != 0 && side != 1 {
		return ErrSide
	}
	return s.put(k, v, ver, true, side)
}
func (s *Store) DelOne(side int, k string, ver int64) error {
	if side != 0 && side != 1 {
		return ErrSide
	}
	return s.del(k, ver, true, side)
}

// Reconcile 只遍历脏集合：逐键取 Ver 大者为胜者（从未写过视作 0），同一份写回两侧。
func (s *Store) Reconcile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k := range s.dirty {
		n++
		ra, oka := s.a[k]
		rb, okb := s.b[k]
		win := ra
		if rec.VerOf(rb, okb) > rec.VerOf(ra, oka) {
			win = rb
		}
		s.a[k], s.b[k] = win, win
		delete(s.dirty, k)
	}
	s.checked = n
}

// View 返回 A 侧活值（对账后 A、B 一致）。
func (s *Store) View() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for k, r := range s.a {
		if !r.Tomb() {
			out[k] = r.Val
		}
	}
	return out
}

// Replicas 返回两侧副本的深拷贝，供自检与演示核验最终一致与幂等。
func (s *Store) Replicas() (a, b map[string]rec.Record) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, b = map[string]rec.Record{}, map[string]rec.Record{}
	for k, r := range s.a {
		a[k] = r
	}
	for k, r := range s.b {
		b[k] = r
	}
	return a, b
}
