package settle

import (
	"ontology/catalog"
	"ontology/fund"
)

// naive 是与生产代码完全独立的逐分朴素模拟器（自带全部校验与拒绝次序）。
type naive struct {
	par params
	now int64

	cat    map[string]catalog.Item
	person map[string]bool
	aid    map[string]bool
	ids    map[string]bool

	acc     map[string]map[int]fund.Acc
	maxYear map[string]int
	active  map[string][]string // 活跃结算 id 栈
	dead    map[string]bool     // 已冲正墓碑
	recs    map[string]*nrec
}

type nrec struct {
	person  string
	year    int
	snap    fund.Acc
	prevMax int
}

func newNaive(par params) *naive {
	return &naive{
		par: par,
		cat: map[string]catalog.Item{
			"jia":  {Class: catalog.Jia, Limit: 10000},
			"yi":   {Class: catalog.Yi, P: 10, Limit: 5000},
			"yi2":  {Class: catalog.Yi, P: 33, Limit: 0},
			"bing": {Class: catalog.Bing},
		},
		person:  map[string]bool{},
		aid:     map[string]bool{},
		ids:     map[string]bool{},
		acc:     map[string]map[int]fund.Acc{},
		maxYear: map[string]int{},
		active:  map[string][]string{},
		dead:    map[string]bool{},
		recs:    map[string]*nrec{},
	}
}

func (n *naive) addPerson(person string) error {
	if person == "" {
		return ErrInvalid
	}
	if n.person[person] {
		return ErrInvalid
	}
	n.person[person] = true
	return nil
}

func (n *naive) setAid(now int64, person string, on bool) error {
	if now < 0 || now > 1e9 || person == "" {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClockBack
	}
	if !n.person[person] {
		return ErrNoPerson
	}
	n.now = now
	n.aid[person] = on
	return nil
}

func nsegments(x0, x, s1, s2 int64) (m1, m2, m3 int64) {
	lo, hi := x0, x0+x
	if hi <= s1 {
		return x, 0, 0
	}
	if lo < s1 {
		m1 = s1 - lo
	}
	upper := hi
	if upper > s2 {
		upper = s2
	}
	lower := lo
	if lower < s1 {
		lower = s1
	}
	if upper > lower {
		m2 = upper - lower
	}
	if hi > s2 {
		low3 := lo
		if low3 < s2 {
			low3 = s2
		}
		m3 = hi - low3
	}
	return m1, m2, m3
}

// settle 朴素结算，返回 Result 与错误；成功后更新内部状态。
func (n *naive) settle(now int64, id, person string, year int, items []Item) (Result, error) {
	var res Result
	if now < 0 || now > 1e9 || id == "" || person == "" || year < 1 || year > 9999 ||
		len(items) < 1 || len(items) > 200 {
		return res, ErrInvalid
	}
	if now < n.now {
		return res, ErrClockBack
	}
	if !n.person[person] {
		return res, ErrNoPerson
	}
	if n.ids[id] {
		return res, ErrDupID
	}
	ents := make([]catalog.Item, len(items))
	for i, it := range items {
		if it.Code == "" || it.Price < 1 || it.Price > 1e9 || it.Qty < 1 || it.Qty > 1e4 {
			return res, ErrInvalid
		}
		ent, ok := n.cat[it.Code]
		if !ok {
			return res, ErrNoItem
		}
		ents[i] = ent
	}
	if year < n.maxYear[person] {
		return res, ErrYearClosed
	}

	n.now = now
	var A, E int64
	for i, it := range items {
		ent := ents[i]
		a := it.Price * it.Qty
		unit := it.Price
		if ent.Limit > 0 && unit > ent.Limit {
			unit = ent.Limit
		}
		b := unit * it.Qty
		var c int64
		switch ent.Class {
		case catalog.Yi:
			c = (b*ent.P + 99) / 100
		case catalog.Bing:
			c = b
		}
		A += a
		E += b - c
	}

	if n.acc[person] == nil {
		n.acc[person] = map[int]fund.Acc{}
	}
	snap := n.acc[person][year]
	prevMax := n.maxYear[person]

	var g int64
	if n.par.d > snap.DU {
		g = E
		if rem := n.par.d - snap.DU; g > rem {
			g = rem
		}
	}
	x := E - g
	m1, m2, m3 := nsegments(snap.X, x, n.par.s1, n.par.s2)
	f := (m1*n.par.r1 + m2*n.par.r2 + m3*n.par.r3) / 100
	f1 := int64(0)
	if n.par.capF > snap.F {
		f1 = f
		if rem := n.par.capF - snap.F; f1 > rem {
			f1 = rem
		}
	}
	pv := E - f1
	yv := imax(0, snap.P+pv-n.par.d2) - imax(0, snap.P-n.par.d2)
	f2 := yv * n.par.rd / 100
	aid := n.aid[person]
	zv := E - f1 - f2
	f3 := int64(0)
	if aid && n.par.capA > snap.Q {
		f3 = zv * n.par.ra / 100
		if rem := n.par.capA - snap.Q; f3 > rem {
			f3 = rem
		}
	}
	self := A - f1 - f2 - f3

	cur := snap
	cur.DU += g
	cur.X += x
	cur.F += f1
	cur.P += pv
	cur.Q += f3
	n.acc[person][year] = cur
	if year > n.maxYear[person] {
		n.maxYear[person] = year
	}
	n.ids[id] = true
	n.active[person] = append(n.active[person], id)
	n.recs[id] = &nrec{person: person, year: year, snap: snap, prevMax: prevMax}

	return Result{A: A, E: E, G: g, X: x, F1: f1, F2: f2, F3: f3, Self: self,
		M1: m1, M2: m2, M3: m3, P: pv, Y: yv, Z: zv, Aid: aid}, nil
}

func (n *naive) reverse(now int64, id string) error {
	if now < 0 || now > 1e9 || id == "" {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClockBack
	}
	r, ok := n.recs[id]
	if !ok {
		return ErrNoSettlement
	}
	// 已冲正：从活跃栈判定墓碑。用单独墓碑集合。
	if n.dead[id] {
		return ErrReversed
	}
	st := n.active[r.person]
	if len(st) == 0 || st[len(st)-1] != id {
		return ErrNotLast
	}
	n.now = now
	n.dead[id] = true
	n.active[r.person] = st[:len(st)-1]
	n.acc[r.person][r.year] = r.snap
	n.maxYear[r.person] = r.prevMax
	return nil
}

func imax(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
