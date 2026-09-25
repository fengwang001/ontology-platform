package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/pred"
	"ontology/rewrite"
)

// ErrInvalidRow is the "参数非法" sentinel: unknown column, bad type, missing column or negative amount.
var ErrInvalidRow = errors.New("api: invalid base row")

type Row map[string]any               // immutable tuple: region:string, amount/year:int
type AggSpec struct{ Group []string } // GROUP BY Group, SUM(amount); nil = grand total
type fmat struct{ rows []Row }
type amat struct{ rows []Row }

type DB struct {
	mu      sync.RWMutex
	base    []Row
	filters map[string]*fmat
	aggs    map[string]*amat
	cat     *rewrite.Catalog
}

func New(rows []Row) (*DB, error) {
	cp := make([]Row, len(rows))
	for i, r := range rows {
		s, ok1 := r[pred.Region].(string)
		a, ok2 := r[pred.Amount].(int)
		_, ok3 := r[pred.Year].(int)
		if len(r) != 3 || !ok1 || !ok2 || !ok3 || a < 0 {
			return nil, ErrInvalidRow
		}
		cp[i] = Row{pred.Region: s, pred.Amount: a, pred.Year: r[pred.Year]}
	}
	return &DB{base: cp, filters: map[string]*fmat{}, aggs: map[string]*amat{}, cat: rewrite.NewCatalog()}, nil
}

func bad(cs []string, amt bool) bool {
	for _, c := range cs {
		if c != pred.Region && c != pred.Year && !(amt && c == pred.Amount) {
			return true
		}
	}
	return false
}
func sv(r Row, c string) string { s, _ := r[c].(string); return s }
func iv(r Row, c string) int    { n, _ := r[c].(int); return n }
func gkey(r Row, g []string) (k string) {
	for _, c := range g {
		k += "|" + fmt.Sprintf("%v", r[c])
	}
	return
}

func sel(rows []Row, p pred.Pred, cs []string) []Row {
	out := []Row{}
	for _, r := range rows {
		if p.Match(sv(r, pred.Region), iv(r, pred.Amount), iv(r, pred.Year)) {
			o := Row{}
			for _, c := range cs {
				o[c] = r[c]
			}
			out = append(out, o)
		}
	}
	sortRows(out)
	return out
}

func aggregate(rows []Row, p pred.Pred, g []string) []Row {
	idx, out := map[string]int{}, []Row{}
	for _, r := range rows {
		if !p.Match(sv(r, pred.Region), iv(r, pred.Amount), iv(r, pred.Year)) {
			continue
		}
		k := gkey(r, g)
		if _, ok := idx[k]; !ok {
			nr := Row{pred.Amount: 0}
			for _, c := range g {
				nr[c] = r[c]
			}
			idx[k] = len(out)
			out = append(out, nr)
		}
		out[idx[k]][pred.Amount] = out[idx[k]][pred.Amount].(int) + iv(r, pred.Amount)
	}
	sortRows(out)
	return out
}

func (d *DB) RegisterFilter(name string, vp pred.Pred, cols []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.cat.RegisterFilter(name, vp, cols); err != nil {
		return err
	}
	d.filters[name] = &fmat{sel(d.base, vp, cols)}
	return nil
}
func (d *DB) RegisterAgg(name string, vp pred.Pred, group []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.cat.RegisterAgg(name, vp, group); err != nil {
		return err
	}
	d.aggs[name] = &amat{aggregate(d.base, vp, group)}
	return nil
}

// Query rewrites through a view when eligible, else full-scans the base table.
func (d *DB) Query(p pred.Pred, proj []string, a *AggSpec) ([]Row, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if bad(proj, true) || (a != nil && bad(a.Group, false)) {
		return nil, rewrite.ErrInvalidColumn
	}
	if a != nil {
		pl := d.cat.MatchAgg(p, a.Group)
		if pl.Full {
			return aggregate(d.base, p, a.Group), nil
		}
		return aggregate(d.aggs[pl.Name].rows, pl.Resid, a.Group), nil
	}
	pl := d.cat.MatchFilter(p, proj)
	if pl.Full {
		return sel(d.base, p, proj), nil
	}
	return sel(d.filters[pl.Name].rows, pl.Resid, proj), nil
}

func sortRows(rs []Row) {
	sort.SliceStable(rs, func(i, j int) bool {
		for _, k := range []string{pred.Region, pred.Year, pred.Amount} {
			x, y := rs[i][k], rs[j][k]
			if x == nil || y == nil || x == y {
				continue
			}
			if s, ok := x.(string); ok {
				return s < y.(string)
			}
			return x.(int) < y.(int)
		}
		return false
	})
}
