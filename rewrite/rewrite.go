// Package rewrite holds view definitions, matching and aggregate roll-up.
package rewrite

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/pred"
)

// ErrInvalidColumn is the "投影/分组列非法" sentinel; distinct from pred.ErrInvalidAtom and the api row error.
var ErrInvalidColumn = errors.New("rewrite: invalid projection/group column")

// Plan is a query decision (Full ⇒ full scan); Name names the chosen view.
type Plan struct {
	Full  bool
	Name  string
	Resid pred.Pred
}
type view struct {
	name string
	vp   pred.Pred
	cols map[string]bool
}

// Catalog buckets views by region: region="v" views live only in v's
// singleton bucket, no-region views in the universe bucket, so a pinned
// query compares just those buckets, never every registered view.
type Catalog struct {
	mu       sync.RWMutex
	fAll     []*view
	fSing    map[string][]*view
	fUniv    []*view
	aAll     []*view
	aSing    map[string][]*view
	aUniv    []*view
	cmpCount atomic.Int64 // unexported; unreachable via any exported identifier
}

func NewCatalog() *Catalog { return &Catalog{fSing: map[string][]*view{}, aSing: map[string][]*view{}} }

func colSet(cols []string, allowAmount bool) (map[string]bool, error) {
	set := map[string]bool{}
	for _, col := range cols { // unknown / amount in GROUP BY / duplicate
		if (col != pred.Region && col != pred.Year && !(allowAmount && col == pred.Amount)) || set[col] {
			return nil, ErrInvalidColumn
		}
		set[col] = true
	}
	return set, nil
}

func cands(all []*view, sing map[string][]*view, univ []*view, p pred.Pred) []*view {
	if p.Region == nil {
		return all
	}
	return append(append([]*view{}, sing[*p.Region]...), univ...)
}

func (c *Catalog) register(name string, vp pred.Pred, cols []string, isAgg bool) error {
	set, err := colSet(cols, !isAgg)
	if err != nil {
		return err
	}
	v := &view{name, vp, set}
	c.mu.Lock()
	defer c.mu.Unlock()
	all, sing, univ := &c.fAll, c.fSing, &c.fUniv
	if isAgg {
		all, sing, univ = &c.aAll, c.aSing, &c.aUniv
	}
	*all = append(*all, v)
	if vp.Region == nil {
		*univ = append(*univ, v)
	} else {
		sing[*vp.Region] = append(sing[*vp.Region], v)
	}
	return nil
}
func (c *Catalog) RegisterFilter(name string, vp pred.Pred, cols []string) error {
	return c.register(name, vp, cols, false)
}
func (c *Catalog) RegisterAgg(name string, vp pred.Pred, group []string) error {
	return c.register(name, vp, group, true)
}

func (c *Catalog) MatchFilter(p pred.Pred, proj []string) Plan {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.cmpCount.Store(0)
	for _, v := range cands(c.fAll, c.fSing, c.fUniv, p) {
		c.cmpCount.Add(1)
		r := pred.Residual(p, v.vp)
		if pred.Contains(p, v.vp) && covers(v.cols, proj) &&
			(r.Region == nil || v.cols[pred.Region]) &&
			(r.Amount == nil || v.cols[pred.Amount]) &&
			(r.Year == nil || v.cols[pred.Year]) {
			return Plan{Name: v.name, Resid: r}
		}
	}
	return Plan{Full: true}
}

func (c *Catalog) MatchAgg(p pred.Pred, gq []string) Plan {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.cmpCount.Store(0)
	if p.Amount != nil { // per-row amount was lost by the aggregate view
		return Plan{Full: true}
	}
	for _, v := range cands(c.aAll, c.aSing, c.aUniv, p) {
		c.cmpCount.Add(1)
		r := pred.Residual(p, v.vp)
		if pred.Contains(p, v.vp) && covers(v.cols, gq) &&
			(r.Region == nil || v.cols[pred.Region]) &&
			(r.Year == nil || v.cols[pred.Year]) {
			return Plan{Name: v.name, Resid: r}
		}
	}
	return Plan{Full: true}
}

func covers(set map[string]bool, cols []string) bool {
	for _, c := range cols {
		if !set[c] {
			return false
		}
	}
	return true
}

// ScaleCheck returns pass/fail only (never the count): pinned query ≤2 comparisons for m∈{100,1000,10000}.
func ScaleCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		c := NewCatalog()
		for i := 0; i < m; i++ {
			s := "r" + strconv.Itoa(i)
			if c.RegisterFilter("v"+s, pred.Pred{Region: &s}, nil) != nil {
				return false
			}
		}
		s := "r" + strconv.Itoa(m/2)
		if pl := c.MatchFilter(pred.Pred{Region: &s}, nil); pl.Name != "v"+s || c.cmpCount.Load() > 2 {
			return false
		}
	}
	return true
}
