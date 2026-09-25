// Package api is the public entry point for the partitioned hash join.
package api

import (
	"errors"
	"reflect"
	"sort"
	"sync"

	"ontology/hjoin"
	"ontology/part"
)

type (
	Key  = part.Key   // join key; must be non-negative
	Pair = hjoin.Pair // one (probe, build) match
)

// Distinct, decidable sentinel errors.
var (
	ErrInvalidN    = errors.New("api: N must be >= 2")
	ErrInvalidM    = errors.New("api: M must be >= 1")
	ErrNegativeKey = errors.New("api: key must be non-negative")
)

// Engine runs one build/probe configuration; RWMutex makes concurrent
// Probe/SelfCheck safe while Build takes the write lock.
type Engine struct {
	mu sync.RWMutex
	j  *hjoin.Join
}

// New constructs an Engine with n >= 2 partitions and threshold m >= 1.
func New(n, m int) (*Engine, error) {
	if n < 2 {
		return nil, ErrInvalidN
	}
	if m < 1 {
		return nil, ErrInvalidM
	}
	return &Engine{j: hjoin.New(n, m)}, nil
}

// Build replaces the build relation; validation precedes any state change.
func (e *Engine) Build(keys []Key) error {
	if err := validate(keys); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.j.Build(keys)
	return nil
}

// Probe joins keys against the built relation; validation precedes it.
func (e *Engine) Probe(keys []Key) ([]Pair, error) {
	if err := validate(keys); err != nil {
		return nil, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.j.Probe(keys), nil
}

func validate(keys []Key) error {
	for _, k := range keys {
		if k < 0 {
			return ErrNegativeKey
		}
	}
	return nil
}

// SelfCheck checks the four invariants: naive equivalence, partition
// consistency, spill invariance, rejected ops leave no trace.
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	cb, cp := []Key{1, 5, 2, 6, 9, 3}, []Key{5, 9, 3, 2, 1, 7}
	sp, _ := New(4, 2)
	if err := sp.Build(cb); err != nil {
		return err
	}
	got, err := sp.Probe(cp)
	if err != nil || len(got) != 5 || !reflect.DeepEqual(keysOf(got), naive(cb, cp)) {
		return errors.New("api: self-check naive/partition consistency failed")
	}
	for s := 0; s < 8; s++ {
		b, p := gen(s)
		lo, _ := New(7, 1)
		hi, _ := New(7, 1_000_000)
		if err := lo.Build(b); err != nil || hi.Build(b) != nil {
			return err
		}
		lp, _ := lo.Probe(p)
		hp, _ := hi.Probe(p)
		if !reflect.DeepEqual(keysOf(lp), keysOf(hp)) || !reflect.DeepEqual(keysOf(lp), naive(b, p)) {
			return errors.New("api: self-check spill invariance failed")
		}
	}
	// Rejected operations: negative probe, negative build, bad N, bad M.
	_, e1 := sp.Probe([]Key{-1})
	e2 := sp.Build([]Key{-1})
	_, e3 := New(1, 1)
	_, e4 := New(2, 0)
	bads := []error{e1, e2, e3, e4}
	wants := []error{ErrNegativeKey, ErrNegativeKey, ErrInvalidN, ErrInvalidM}
	for i := range bads {
		if !errors.Is(bads[i], wants[i]) {
			return errors.New("api: self-check rejection failed")
		}
	}
	if again, _ := sp.Probe(cp); len(again) != 5 {
		return errors.New("api: self-check state changed after rejection")
	}
	return nil
}

func keysOf(ps []Pair) []int {
	o := make([]int, len(ps))
	for i, q := range ps {
		o[i] = int(q.P)
	}
	sort.Ints(o)
	return o
}

func naive(b, p []Key) []int {
	o := []int{}
	for _, pk := range p {
		for _, bk := range b {
			if pk == bk {
				o = append(o, int(pk))
			}
		}
	}
	sort.Ints(o)
	return o
}

func gen(s int) (b, p []Key) {
	for i := 0; i < s*3+1; i++ {
		b = append(b, Key((i*7+s)%23))
	}
	for i := 0; i < s*2+1; i++ {
		p = append(p, Key((i*11+s)%29))
	}
	return b, p
}
