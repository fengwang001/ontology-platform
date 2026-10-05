// Package mrp 按低层码逐层展开分期净需求，并提供需求追溯（pegging）。
package mrp

import (
	"errors"
	"math/big"
	"sort"
	"sync"

	"ontology/bom"
	"ontology/stock"
)

var (
	ErrInvalid  = errors.New("mrp: invalid parameter")
	ErrNotExist = errors.New("mrp: item does not exist")
	ErrConflict = errors.New("mrp: item or relation already exists")
	ErrCycle    = errors.New("mrp: relation would create a cycle")
	ErrOverflow = errors.New("mrp: intermediate value exceeds 1e15")
	ErrStale    = errors.New("mrp: result is stale")
)

const (
	maxT   = 52
	maxQty = int64(1_000_000_000)
	satCap = int64(1) << 62
)

var overflowLimit = big.NewInt(1_000_000_000_000_000)

// Exception 记录计划投放期早于第 1 期的欠缺情况（物料, 期, 欠缺期数）。
type Exception struct {
	Item   string
	Period int
	Short  int
}

// PegSource 是某期毛需求中一个父项的贡献。
type PegSource struct {
	Parent string
	Qty    int64
}

// Peg 是某物料某期毛需求的来源分解：独立需求量加各父项贡献。
type Peg struct {
	Independent int64
	Sources     []PegSource
}

// Result 保存一次 Run 的完整分期结果。数组下标 1..T，A[0] 为期初库存。
type Result struct {
	T          int
	Items      []string
	G          map[string][]int64
	Rec        map[string][]int64
	Rel        map[string][]int64
	A          map[string][]int64
	Exceptions []Exception
}

// Engine 是组合 stock.Register 与 bom.Graph 的计算门面，并发安全。
type Engine struct {
	mu     sync.Mutex
	t      int
	st     *stock.Register
	bg     *bom.Graph
	demand map[string][]int64
	sched  map[string][]int64
	mutVer int64

	res       *Result
	pegs      map[string][]Peg
	snapStock int64
	snapBom   int64
	snapMut   int64
	hasRun    bool

	visitedItems int
	visitedEdges int
}

func New(t int, st *stock.Register, bg *bom.Graph) (*Engine, error) {
	if t < 1 || t > maxT || st == nil || bg == nil {
		return nil, ErrInvalid
	}
	return &Engine{
		t:      t,
		st:     st,
		bg:     bg,
		demand: make(map[string][]int64),
		sched:  make(map[string][]int64),
	}, nil
}

func (e *Engine) AddItem(name string, onHand, ss int64, lead int, lotMin, lotMult int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch err := e.st.AddItem(name, onHand, ss, lead, lotMin, lotMult); {
	case err == nil:
		return nil
	case errors.Is(err, stock.ErrConflict):
		return ErrConflict
	default:
		return ErrInvalid
	}
}

func (e *Engine) AddComponent(parent, child string, per, scrap int64) error {
	if !stock.ValidName(parent) || !stock.ValidName(child) ||
		per < 1 || per > 10_000 || scrap < 0 || scrap > 999 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.st.Get(parent); !ok {
		return ErrNotExist
	}
	if _, ok := e.st.Get(child); !ok {
		return ErrNotExist
	}
	switch err := e.bg.AddComponent(parent, child, per, scrap); {
	case err == nil:
		return nil
	case errors.Is(err, bom.ErrConflict):
		return ErrConflict
	case errors.Is(err, bom.ErrCycle):
		return ErrCycle
	default:
		return ErrInvalid
	}
}

func (e *Engine) Demand(item string, t int, qty int64) error {
	if !stock.ValidName(item) || t < 1 || t > e.t || qty < 1 || qty > maxQty {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.st.Get(item); !ok {
		return ErrNotExist
	}
	a := e.demand[item]
	if a == nil {
		a = make([]int64, e.t+1)
		e.demand[item] = a
	}
	a[t] = satAdd(a[t], qty)
	e.mutVer++
	return nil
}

func (e *Engine) Scheduled(item string, t int, qty int64) error {
	if !stock.ValidName(item) || t < 1 || t > e.t || qty < 1 || qty > maxQty {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.st.Get(item); !ok {
		return ErrNotExist
	}
	a := e.sched[item]
	if a == nil {
		a = make([]int64, e.t+1)
		e.sched[item] = a
	}
	a[t] = satAdd(a[t], qty)
	e.mutVer++
	return nil
}

// Run 按低层码升序处理全部物料，返回分期结果；任一中间量超过 1e15 报溢出。
func (e *Engine) Run() (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	items := e.st.All()
	codes := e.bg.LowCodes()
	sort.Slice(items, func(i, j int) bool {
		ci, cj := codes[items[i].Name], codes[items[j].Name]
		if ci != cj {
			return ci < cj
		}
		return items[i].Name < items[j].Name
	})

	res := &Result{
		T:     e.t,
		Items: make([]string, 0, len(items)),
		G:     make(map[string][]int64, len(items)),
		Rec:   make(map[string][]int64, len(items)),
		Rel:   make(map[string][]int64, len(items)),
		A:     make(map[string][]int64, len(items)),
	}
	pegs := make(map[string][]Peg, len(items))
	rels := make(map[string][]*big.Int, len(items))

	e.visitedItems = 0
	e.visitedEdges = 0

	den := new(big.Int)
	for _, it := range items {
		e.visitedItems++
		parents := e.bg.Parents(it.Name)
		e.visitedEdges += len(parents)

		g := make([]int64, e.t+1)
		rec := make([]int64, e.t+1)
		rel := make([]int64, e.t+1)
		avail := make([]int64, e.t+1)
		relSum := make([]*big.Int, e.t+1)
		for i := range relSum {
			relSum[i] = new(big.Int)
		}
		dayPegs := make([]Peg, e.t+1)

		avail[0] = it.OnHand
		prev := big.NewInt(it.OnHand)
		for tp := 1; tp <= e.t; tp++ {
			gt := new(big.Int).SetInt64(at(e.demand[it.Name], tp))
			dayPegs[tp].Independent = at(e.demand[it.Name], tp)
			for _, p := range parents {
				pr := rels[p.Parent][tp]
				if pr.Sign() == 0 {
					continue
				}
				num := new(big.Int).Mul(pr, big.NewInt(p.Per*1000))
				den.SetInt64(1000 - p.Scrap)
				num.Add(num, den).Sub(num, big.NewInt(1)).Quo(num, den)
				if overLimit(num) {
					return nil, ErrOverflow
				}
				dayPegs[tp].Sources = append(dayPegs[tp].Sources,
					PegSource{Parent: p.Parent, Qty: num.Int64()})
				gt.Add(gt, num)
			}
			if overLimit(gt) {
				return nil, ErrOverflow
			}
			g[tp] = gt.Int64()

			x := new(big.Int).Add(prev, big.NewInt(at(e.sched[it.Name], tp)))
			x.Sub(x, gt)
			if overLimit(x) {
				return nil, ErrOverflow
			}
			recT := new(big.Int)
			if x.Cmp(big.NewInt(it.SS)) < 0 {
				net := new(big.Int).SetInt64(it.SS)
				net.Sub(net, x)
				if overLimit(net) {
					return nil, ErrOverflow
				}
				recT = lotBig(net, it.LotMin, it.LotMult)
				if overLimit(recT) {
					return nil, ErrOverflow
				}
			}
			rec[tp] = recT.Int64()
			cur := new(big.Int).Add(x, recT)
			if overLimit(cur) {
				return nil, ErrOverflow
			}
			avail[tp] = cur.Int64()
			prev = cur

			if recT.Sign() > 0 {
				rt := tp - it.Lead
				slot := rt
				if slot < 1 {
					slot = 1
				}
				relSum[slot].Add(relSum[slot], recT)
				if overLimit(relSum[slot]) {
					return nil, ErrOverflow
				}
				if rt < 1 {
					res.Exceptions = append(res.Exceptions,
						Exception{Item: it.Name, Period: tp, Short: 1 - rt})
				}
			}
		}
		for tp := 1; tp <= e.t; tp++ {
			rel[tp] = relSum[tp].Int64()
		}
		res.Items = append(res.Items, it.Name)
		res.G[it.Name] = g
		res.Rec[it.Name] = rec
		res.Rel[it.Name] = rel
		res.A[it.Name] = avail
		pegs[it.Name] = dayPegs
		rels[it.Name] = relSum
	}

	sort.Slice(res.Exceptions, func(i, j int) bool {
		a, b := res.Exceptions[i], res.Exceptions[j]
		if a.Item != b.Item {
			return a.Item < b.Item
		}
		return a.Period < b.Period
	})

	e.res = res
	e.pegs = pegs
	e.snapStock = e.st.Version()
	e.snapBom = e.bg.Version()
	e.snapMut = e.mutVer
	e.hasRun = true
	return res, nil
}

// Peg 返回最近一次 Run 中 item 第 t 期毛需求的来源分解。
func (e *Engine) Peg(item string, t int) (Peg, error) {
	if !stock.ValidName(item) || t < 1 || t > e.t {
		return Peg{}, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.st.Get(item); !ok {
		return Peg{}, ErrNotExist
	}
	if !e.hasRun || e.snapStock != e.st.Version() ||
		e.snapBom != e.bg.Version() || e.snapMut != e.mutVer {
		return Peg{}, ErrStale
	}
	p := e.pegs[item][t]
	return Peg{Independent: p.Independent, Sources: append([]PegSource(nil), p.Sources...)}, nil
}

func at(a []int64, i int) int64 {
	if a == nil {
		return 0
	}
	return a[i]
}

func satAdd(a, b int64) int64 {
	if a >= satCap-b {
		return satCap
	}
	return a + b
}

func overLimit(v *big.Int) bool {
	return v.CmpAbs(overflowLimit) > 0
}

func lotBig(net *big.Int, lotMin, lotMult int64) *big.Int {
	m := big.NewInt(lotMult)
	q := new(big.Int).Add(net, m)
	q.Sub(q, big.NewInt(1)).Quo(q, m).Mul(q, m)
	if min := big.NewInt(lotMin); q.Cmp(min) < 0 {
		q.Set(min)
	}
	return q
}
