package stats

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/ws"
)

var ErrEmptyKey = errors.New("stats: key must not be empty")
var ErrValueAbsent = errors.New("stats: no element with that value in group")
var ErrMergeEmpty = errors.New("stats: merge source group is empty")
var ErrMergeSelf = errors.New("stats: merge source must differ from target")
var ErrSelfCheck = errors.New("stats: self-check failed")

const OpAdd, OpRemove, OpMerge = 1, 2, 3

type Op struct {
	Kind       int
	Key, Other string
	Value      float64
}

type View struct {
	N                       int64
	Mean, M2, Variance, Std float64
}

type Store struct {
	mu     sync.RWMutex
	st     map[string]*ws.State
	counts map[string]map[float64]int64
	probes int64 // unexported hash-lookup count from the latest Apply
}

func NewStore() *Store {
	return &Store{st: map[string]*ws.State{}, counts: map[string]map[float64]int64{}}
}

func (s *Store) ensure(k string) *ws.State {
	g := s.st[k]
	if g == nil {
		g = ws.New()
		s.st[k] = g
		s.counts[k] = map[float64]int64{}
	}
	return g
}

func (s *Store) Apply(op Op) error {
	s.mu.Lock()
	s.probes = 0
	defer s.mu.Unlock()
	if op.Key == "" {
		return ErrEmptyKey
	}
	switch op.Kind {
	case OpAdd:
		g := s.ensure(op.Key)
		g.Add(op.Value)
		s.counts[op.Key][op.Value]++
	case OpRemove:
		g := s.st[op.Key]
		s.probes++ // exactly one hash lookup, independent of size
		c, ok := s.counts[op.Key][op.Value]
		if g == nil || !ok {
			return ErrValueAbsent
		}
		g.Remove(op.Value)
		if c > 1 {
			s.counts[op.Key][op.Value] = c - 1
		} else {
			delete(s.counts[op.Key], op.Value)
		}
	case OpMerge:
		og := s.st[op.Other]
		if op.Other == op.Key {
			return ErrMergeSelf
		}
		if og == nil || og.Count() == 0 {
			return ErrMergeEmpty
		}
		g := s.ensure(op.Key)
		snap := *og
		g.Merge(&snap)
		for x, c := range s.counts[op.Other] {
			s.counts[op.Key][x] += c
		}
	default:
		return fmt.Errorf("stats: unknown op kind %d", op.Kind)
	}
	return nil
}

func (s *Store) View(k string) View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g := s.st[k]
	if g == nil || g.Count() == 0 {
		return View{}
	}
	return View{g.Count(), g.Mean(), g.M2(), g.Variance(), g.Std()}
}

func (s *Store) SelfCheck() error {
	c := NewStore()
	for _, x := range []float64{10, 20} {
		c.Apply(Op{OpAdd, "G2", "", x})
	}
	ops := []Op{
		{OpAdd, "G", "", 1}, {OpAdd, "G", "", 2}, {OpAdd, "G", "", 3}, {OpAdd, "G", "", 5},
		{OpRemove, "G", "", 1}, {OpAdd, "G", "", 4}, {OpRemove, "G", "", 3}, {OpMerge, "G", "G2", 0},
	}
	want := []View{
		{1, 1, 0, 0, 0}, {2, 1.5, .5, .25, .5}, {3, 2, 2, 2. / 3, 0}, {4, 2.75, 8.75, 2.1875, 0},
		{3, 10. / 3, 14. / 3, 14. / 9, 0}, {4, 3.5, 5, 1.25, 0}, {3, 11. / 3, 14. / 3, 14. / 9, 0}, {5, 8.2, 208.8, 41.76, 0},
	}
	for i, op := range ops {
		if err := c.Apply(op); err != nil || !viewClose(c.View("G"), want[i]) {
			return ErrSelfCheck
		}
	}
	bad := []Op{{OpAdd, "", "", 0}, {OpRemove, "G", "", 999}, {OpMerge, "G", "VOID", 0}, {OpMerge, "G", "G", 0}}
	before := c.View("G")
	seen := map[error]bool{}
	for _, o := range bad {
		err := c.Apply(o)
		if err == nil || seen[err] || c.View("G") != before {
			return ErrSelfCheck
		}
		seen[err] = true
	}
	for _, m := range []int{100, 1000, 10000} {
		k := fmt.Sprintf("L%d", m)
		for i := 0; i < m; i++ {
			c.Apply(Op{OpAdd, k, "", float64(i)})
		}
		if c.Apply(Op{OpRemove, k, "", 42}) != nil || c.probes != 1 {
			return ErrSelfCheck
		}
	}
	return c.Apply(Op{OpAdd, "G", "", 7})
}

func viewClose(a, b View) bool {
	rf := func(x, y float64) bool { return math.Abs(x-y) <= 1e-9*math.Max(1, math.Abs(y)) }
	return a.N == b.N && rf(a.Mean, b.Mean) && rf(a.M2, b.M2) && rf(a.Variance, b.Variance)
}
