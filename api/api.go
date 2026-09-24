// Package api is the public facade of the exact-quantile multiset engine.
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/qnt"
)

// Distinguishable sentinel errors, one per rejection cause.
var (
	ErrBadConfig = errors.New("api: maxValues must be positive")
	ErrCapacity  = errors.New("api: element count would exceed maxValues")
	ErrNotFound  = errors.New("api: value not present")
	ErrEmpty     = errors.New("api: empty multiset")
)

// Engine is a concurrency-safe exact-quantile multiset.
type Engine struct {
	mu  sync.RWMutex
	eng qnt.Engine
	max int
}

func New(maxValues int) (*Engine, error) {
	if maxValues <= 0 {
		return nil, ErrBadConfig
	}
	return &Engine{max: maxValues}, nil
}
func (e *Engine) Insert(v int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.eng.Count() >= e.max {
		return ErrCapacity
	}
	e.eng.Insert(v)
	return nil
}
func (e *Engine) Delete(v int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.eng.Delete(v); err != nil {
		return ErrNotFound
	}
	return nil
}
func (e *Engine) Median() (float64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, err := e.eng.Median()
	return m, mapErr(err)
}
func (e *Engine) QuantileP90() (int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	q, err := e.eng.QuantileP90()
	return q, mapErr(err)
}
func (e *Engine) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.eng.Count()
}
func mapErr(err error) error {
	if err != nil {
		return ErrEmpty
	}
	return nil
}
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !qnt.VisitBoundOK() {
		return errors.New("api: visit bound violated")
	}
	var eng qnt.Engine
	var model []int64
	for i := 0; i < 600; i++ {
		if len(model) == 0 || i%3 != 2 { // deterministic pseudo-random mix
			v := int64((i*37+11)%97) - 48
			eng.Insert(v)
			model = append(model, v)
		} else {
			j := (i * 13) % len(model)
			if err := eng.Delete(model[j]); err != nil {
				return fmt.Errorf("selfcheck delete: %w", err)
			}
			model = slices.Delete(model, j, j+1)
		}
		if err := checkModel(&eng, model); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i, err)
		}
	}
	full, _ := New(1)
	_ = full.Insert(7)
	empty, _ := New(4)
	_, badCfg := New(0)
	_, emptyMed := empty.Median()
	for i, r := range []struct{ got, want error }{
		{full.Insert(8), ErrCapacity}, {full.Delete(9), ErrNotFound}, {badCfg, ErrBadConfig}, {emptyMed, ErrEmpty},
	} {
		if !errors.Is(r.got, r.want) {
			return fmt.Errorf("selfcheck: rejection %d missing", i)
		}
	}
	if full.Count() != 1 {
		return errors.New("selfcheck: rejected op mutated state")
	}
	return nil
}

func checkModel(eng *qnt.Engine, model []int64) error {
	s := slices.Clone(model)
	slices.Sort(s)
	if eng.Count() != len(s) {
		return errors.New("count mismatch")
	}
	if len(s) == 0 {
		_, e1 := eng.Median()
		_, e2 := eng.QuantileP90()
		if !errors.Is(e1, qnt.ErrEmpty) || !errors.Is(e2, qnt.ErrEmpty) {
			return errors.New("empty query not rejected")
		}
		return nil
	}
	for k := 1; k <= len(s); k++ {
		if v, err := eng.Kth(k); err != nil || v != s[k-1] {
			return fmt.Errorf("kth(%d) mismatch", k)
		}
	}
	med, err1 := eng.Median()
	p90, err2 := eng.QuantileP90()
	if err1 != nil || err2 != nil {
		return errors.New("query failed")
	}
	n := len(s)
	want := float64(s[n/2])
	if n%2 == 0 {
		want = float64(s[n/2-1])/2 + float64(s[n/2])/2
	}
	if med != want || p90 != s[(90*n+99)/100-1] || med > float64(p90) {
		return errors.New("median/p90 mismatch")
	}
	return nil
}
