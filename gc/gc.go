// Package gc 实现多副本变更流合并、稳定向量与因果稳定墓碑回收，只依赖 vv。
package gc

import (
	"container/heap"
	"errors"
	"slices"
	"sort"
	"sync"

	"ontology/vv"
)

var (
	ErrInvalidR = errors.New("gc: R must be positive")
	ErrReplica  = errors.New("gc: replica id out of range")
	ErrVector   = errors.New("gc: negative component or bad length")
	ErrKey      = errors.New("gc: empty key")
)

type Entry struct {
	Vec  vv.Vector
	Val  string
	Tomb bool
}
type Kind uint8
type Op struct {
	Kind    Kind
	Replica int
	Key     string
	Value   string
	Vec     vv.Vector
}

type tomb struct {
	key string
	vec vv.Vector
	mv  int
}
type Store struct {
	mu     sync.RWMutex
	clk    []vv.Vector
	store  map[string]Entry
	th     tHeap
	gone   map[string]vv.Vector
	probeN int
}
type tHeap []tomb

const KindWrite, KindDelete, KindSync = Kind(1), Kind(2), Kind(3)

func New(r int) (*Store, error) {
	if r <= 0 {
		return nil, ErrInvalidR
	}
	return &Store{clk: slices.Repeat([]vv.Vector{make(vv.Vector, r)}, r), store: map[string]Entry{}, gone: map[string]vv.Vector{}}, nil
}
func (s *Store) check(rep int, key string, v vv.Vector, needKey bool) error {
	switch {
	case rep < 0 || rep >= len(s.clk):
		return ErrReplica
	case !vv.Valid(v, len(s.clk)):
		return ErrVector
	case needKey && key == "":
		return ErrKey
	}
	return nil
}
func (s *Store) merge(key string, v vv.Vector, val string, isTomb bool) {
	g, inGone := s.gone[key]
	e, inStore := s.store[key]
	if inGone && vv.Cmp(v, g) <= 0 || inStore && vv.Cmp(v, e.Vec) <= 0 {
		return
	}
	cp := append(vv.Vector(nil), v...)
	s.store[key] = Entry{Vec: cp, Val: val, Tomb: isTomb}
	if isTomb {
		heap.Push(&s.th, tomb{key: key, vec: cp, mv: slices.Max(cp)})
	}
}
func (s *Store) Write(rep int, key, val string, v vv.Vector) error {
	return s.Apply([]Op{{KindWrite, rep, key, val, v}})
}
func (s *Store) Delete(rep int, key string, v vv.Vector) error {
	return s.Apply([]Op{{KindDelete, rep, key, "", v}})
}
func (s *Store) Sync(rep int, v vv.Vector) error { return s.Apply([]Op{{KindSync, rep, "", "", v}}) }
func (s *Store) Stable() vv.Vector               { s.mu.RLock(); defer s.mu.RUnlock(); return vv.MinPointwise(s.clk) }
func (s *Store) View() map[string]Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Entry, len(s.store))
	for k, e := range s.store {
		out[k] = Entry{Vec: append(vv.Vector(nil), e.Vec...), Val: e.Val, Tomb: e.Tomb}
	}
	return out
}
func (s *Store) Apply(ops []Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range ops {
		if err := s.check(o.Replica, o.Key, o.Vec, o.Kind != KindSync); err != nil {
			return err
		}
	}
	for _, o := range ops {
		if o.Kind != KindSync {
			s.merge(o.Key, o.Vec, o.Value, o.Kind == KindDelete)
		}
		s.clk[o.Replica] = vv.MergeMax(s.clk[o.Replica], o.Vec)
	}
	return nil
}
func (s *Store) GC() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := vv.MinPointwise(s.clk)
	m := slices.Max(st)
	s.probeN = 0
	var cand []tomb
	for len(s.th) > 0 && s.th[0].mv <= m {
		t := heap.Pop(&s.th).(tomb)
		s.probeN++
		if e, ok := s.store[t.key]; ok && e.Tomb && vv.Cmp(e.Vec, t.vec) == 0 {
			cand = append(cand, t)
		}
	}
	var rem []string
	for _, t := range cand {
		if vv.LeqPointwise(t.vec, st) {
			s.gone[t.key] = append(vv.Vector(nil), t.vec...)
			delete(s.store, t.key)
			rem = append(rem, t.key)
		} else {
			heap.Push(&s.th, t)
		}
	}
	sort.Strings(rem)
	return rem
}
func (h tHeap) Len() int      { return len(h) }
func (h tHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *tHeap) Push(x any)   { *h = append(*h, x.(tomb)) }
func (h *tHeap) Pop() any     { t := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return t }
func (h tHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	return a.mv < b.mv || (a.mv == b.mv && (vv.Less(a.vec, b.vec) || vv.Cmp(a.vec, b.vec) == 0 && a.key < b.key))
}
