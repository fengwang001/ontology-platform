// Package api is the public entry point: in-memory rows, views, rewriting.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/pred"
	"ontology/rewrite"
)

type Row map[string]any
type AggSpec struct{ Group []string }
type Engine struct {
	mu       sync.RWMutex
	c        *rewrite.Catalog
	baseRows []rewrite.Row
}

var ErrBadColumn = errors.New("invalid projection or group column")
var ErrBadPredicate = errors.New("invalid predicate column or operator")
var ErrBadRows = errors.New("invalid base rows: unknown column or negative amount")
var colIdx = map[string]int{"region": 0, "amount": 1, "year": 2}

func New(rows []Row) (*Engine, error) {
	br := make([]rewrite.Row, 0, len(rows))
	for _, r := range rows {
		s, sok := r["region"].(string)
		n, nok := r["amount"].(int)
		y, yok := r["year"].(int)
		for k := range r {
			if k != "region" && k != "amount" && k != "year" {
				return nil, ErrBadRows
			}
		}
		if r["region"] != nil && !sok || r["amount"] != nil && (!nok || n < 0) ||
			r["year"] != nil && !yok {
			return nil, ErrBadRows
		}
		br = append(br, rewrite.Row{Region: s, Amount: n, Year: y})
	}
	return &Engine{c: rewrite.NewCatalog(br), baseRows: br}, nil
}
func maskOf(cols []string, allow [3]bool) (m [3]bool, err error) {
	for _, c := range cols {
		i, ok := colIdx[c]
		if !ok || !allow[i] {
			return m, ErrBadColumn
		}
		m[i] = true
	}
	return m, nil
}
func prep(p pred.Pred, pj []string, sp *AggSpec) (m [3]bool, a *rewrite.Agg, err error) {
	if !pred.Valid(p) {
		return m, nil, ErrBadPredicate
	}
	if m, err = maskOf(pj, [3]bool{true, true, true}); err != nil {
		return
	}
	if sp != nil {
		g, e := maskOf(sp.Group, [3]bool{true, false, true})
		if e != nil {
			return m, nil, e
		}
		a = &rewrite.Agg{Region: g[0], Year: g[2]}
	}
	return m, a, nil
}
func (e *Engine) RegisterFilter(name string, vp pred.Pred, cols []string) error {
	m, _, err := prep(vp, cols, nil)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.c.Add(name, vp, m, [3]bool{}, false)
	return nil
}
func (e *Engine) RegisterAgg(name string, vp pred.Pred, group []string) error {
	_, a, err := prep(vp, nil, &AggSpec{Group: group})
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.c.Add(name, vp, [3]bool{}, [3]bool{a.Region, false, a.Year}, true)
	return nil
}
func (e *Engine) Query(p pred.Pred, proj []string, agg *AggSpec) ([]Row, error) {
	m, a, err := prep(p, proj, agg)
	if err != nil {
		return nil, err
	}
	keep := m
	if a != nil {
		keep = [3]bool{a.Region, true, a.Year}
	}
	e.mu.RLock()
	rs, _, _ := e.c.Query(p, m, a)
	e.mu.RUnlock()
	out := make([]Row, len(rs))
	for i, r := range rs {
		row := Row{"region": r.Region, "amount": r.Amount, "year": r.Year}
		for j, n := range [3]string{"region", "amount", "year"} {
			if !keep[j] {
				delete(row, n)
			}
		}
		out[i] = row
	}
	return out, nil
}
func (e *Engine) SelfCheck() error {
	fail := errors.New("self-check failed")
	bare := rewrite.NewCatalog(e.baseRows)
	eq := func(p pred.Pred, pj []string, sp *AggSpec) bool {
		m, a, _ := prep(p, pj, sp)
		got, _, _ := e.c.Query(p, m, a)
		want, _, _ := bare.Query(p, m, a)
		return reflect.DeepEqual(got, want)
	}
	for _, p := range []pred.Pred{{}, {Amount: pred.IGe(100)}, {Amount: pred.IGt(100)},
		{Region: pred.RegionEq("east")}, {Region: pred.RegionEq("east"), Amount: pred.IGe(100)}} {
		for _, pj := range [][]string{{"region", "amount"}, {"region", "amount", "year"}} {
			if !eq(p, pj, nil) {
				return fail
			}
		}
		for _, g := range [][]string{nil, []string{}, {"region"}, {"region", "year"}} {
			if !eq(p, nil, &AggSpec{Group: g}) {
				return fail
			}
		}
	}
	_, e1 := New([]Row{{"region": 1}})
	_, e2 := e.Query(pred.Pred{}, []string{"x"}, nil)
	_, e3 := e.Query(pred.Pred{Region: pred.Atom{Op: pred.Lt}}, nil, nil)
	if e1 != ErrBadRows || e2 != ErrBadColumn || e3 != ErrBadPredicate ||
		!eq(pred.Pred{Amount: pred.IGt(0)}, []string{"region", "amount"}, nil) {
		return fail
	}
	return nil
}
