// Package rewrite holds materialized views and rewrites queries to use them.
package rewrite

import (
	"cmp"
	"slices"
	"strings"
	"sync/atomic"

	"ontology/pred"
)

type Row struct {
	Region       string
	Amount, Year int
}
type Agg struct{ Region, Year bool }
type view struct {
	name      string
	vp        pred.Pred
	cols, grp [3]bool // cols: projected region/amount/year; grp slots 0,2 = region,year
	anyR, agg bool
	mat       []Row
	sums      map[Row]int
}
type Catalog struct {
	base   []Row
	views  []*view
	bucket map[string][]*view // region-equality hash index
	anyV   []*view            // views without a region atom
	// compares counts full predicate comparisons in the latest Query; it is
	// unexported and the hash buckets bound it independently of view count.
	compares atomic.Int64
}

func NewCatalog(base []Row) *Catalog {
	return &Catalog{base: slices.Clone(base), bucket: map[string][]*view{}}
}
func (c *Catalog) Add(name string, vp pred.Pred, cols, grp [3]bool, isAgg bool) {
	v := &view{name: name, vp: vp, cols: cols, grp: grp,
		anyR: vp.Region.Op != pred.Eq, agg: isAgg, sums: map[Row]int{}}
	for _, r := range c.base {
		if v.vp.Match(r.Region, r.Amount, r.Year) {
			if v.agg {
				v.sums[mask(r, grp)] += r.Amount
			} else {
				v.mat = append(v.mat, mask(r, cols))
			}
		}
	}
	c.views = append(c.views, v)
	if v.anyR {
		c.anyV = append(c.anyV, v)
	} else {
		c.bucket[vp.Region.Str] = append(c.bucket[vp.Region.Str], v)
	}
}
func mask(r Row, k [3]bool) Row { // keep a slot or reset it to "" / 0
	return Row{map[bool]string{true: r.Region}[k[0]],
		map[bool]int{true: r.Amount}[k[1]], map[bool]int{true: r.Year}[k[2]]}
}
func covers(cols, need [3]bool) bool {
	for i := range need {
		if need[i] && !cols[i] {
			return false
		}
	}
	return true
}
func (c *Catalog) pick(p pred.Pred, isAgg bool, proj [3]bool, g Agg) *view {
	cs := c.views // equality bucket first, then region-free views
	if p.Region.Op == pred.Eq {
		cs = append(append([]*view{}, c.bucket[p.Region.Str]...), c.anyV...)
	}
	for _, v := range cs {
		if v.agg != isAgg {
			continue
		}
		c.compares.Add(1)
		if !pred.Contains(p, v.vp) {
			continue
		}
		res := pred.Residual(p, v.vp)
		if isAgg {
			// Gq ⊆ Gv; reject residual on a column the aggregate view cannot
			// filter per output group (amount always lost; an ungrouped key).
			if (!g.Region || v.grp[0]) && (!g.Year || v.grp[2]) && !res.Amount.Present() &&
				(!res.Region.Present() || v.grp[0]) && (!res.Year.Present() || v.grp[2]) {
				return v
			}
			continue
		}
		rn := [3]bool{res.Region.Present(), res.Amount.Present(), res.Year.Present()}
		if covers(v.cols, proj) && covers(v.cols, rn) {
			return v
		}
	}
	return nil
}

// Query rewrites one query; name == "" means full scan, res is the residual.
func (c *Catalog) Query(p pred.Pred, proj [3]bool, agg *Agg) (rows []Row, name string, res pred.Pred) {
	c.compares.Store(0)
	if agg != nil {
		return c.queryAgg(p, *agg)
	}
	res, src := p, c.base
	if v := c.pick(p, false, proj, Agg{}); v != nil {
		src, name, res = v.mat, v.name, pred.Residual(p, v.vp)
	}
	for _, r := range src {
		if res.Match(r.Region, r.Amount, r.Year) {
			rows = append(rows, mask(r, proj))
		}
	}
	sortRows(rows)
	return
}
func (c *Catalog) queryAgg(p pred.Pred, g Agg) (rows []Row, name string, _ pred.Pred) {
	keep := [3]bool{g.Region, false, g.Year}
	sums := map[Row]int{}
	if v := c.pick(p, true, [3]bool{}, g); v != nil { // roll finer groups up
		name = v.name
		res := pred.Residual(p, v.vp)
		for k, s := range v.sums {
			if res.Match(k.Region, 0, k.Year) { // filter retained groups, regroup
				sums[mask(k, keep)] += s
			}
		}
	} else { // full scan of the base table
		for _, r := range c.base {
			if p.Match(r.Region, r.Amount, r.Year) {
				sums[mask(r, keep)] += r.Amount
			}
		}
	}
	rows = make([]Row, 0, len(sums))
	for k, s := range sums {
		k.Amount = s
		rows = append(rows, k)
	}
	sortRows(rows)
	return
}
func sortRows(rs []Row) { // region, then year, then amount, ascending
	slices.SortFunc(rs, func(a, b Row) int {
		return cmp.Or(strings.Compare(a.Region, b.Region),
			cmp.Compare(a.Year, b.Year), cmp.Compare(a.Amount, b.Amount))
	})
}
