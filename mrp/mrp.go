// Package mrp 按低层码逐层展开分期净需求，产出毛需求、计划到货、计划投放、
// 预计可用量、例外清单与需求追溯。
package mrp

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"sync"

	"ontology/bom"
	"ontology/stock"
)

// 重导出下层包的哨兵错误，并定义本包的结果过期与溢出错误。
var (
	ErrInvalid  = stock.ErrInvalid
	ErrNotExist = stock.ErrNotExist
	ErrConflict = stock.ErrConflict
	ErrCycle    = bom.ErrCycle
	ErrStale    = errors.New("result is stale")
	ErrOverflow = errors.New("intermediate quantity exceeds 1e15")
)

const (
	maxT   = 52
	maxQty = 1_000_000_000         // Demand/Scheduled 单次数量上限 1e9
	limit  = 1_000_000_000_000_000 // 中间量上限 1e15
)

// ItemResult 为单个物料的分期结果；切片下标即期号（1..T），0 号位不用（A[0] 为期初库存）。
type ItemResult struct {
	G   []uint64 // 毛需求
	Rec []uint64 // 计划到货
	Rel []uint64 // 计划投放
	A   []uint64 // 期末预计可用量
}

// Exception 为一条逾期投放例外：投放被压到第 1 期，欠缺 Short 期。
type Exception struct {
	Item  string
	T     uint64
	Short uint64
}

// PegEntry 为毛需求的一笔父项来源。
type PegEntry struct {
	Parent string
	Qty    uint64
}

// PegInfo 为某物料某期毛需求的来源：独立需求量加各父项贡献（按父项字节序）。
type PegInfo struct {
	Independent uint64
	Parents     []PegEntry
}

// Result 为一次 Run 的完整结果。
type Result struct {
	T          uint64
	Order      []string // 处理顺序：低层码升序，同码按物料号字节序
	Items      map[string]*ItemResult
	Exceptions []Exception // 按物料号字节序、再按 t 升序

	independent  map[string][]uint64
	pegs         map[string][][]PegEntry
	visitedItems int
	visitedEdges int
}

// Engine 组合物料登记与父子关系，计算分期净需求。所有方法可并发调用，
// 结果等价于某个串行顺序。
type Engine struct {
	mu      sync.Mutex
	t       uint64
	st      *stock.Stock
	bm      *bom.BOM
	demand  map[string][]uint64
	sched   map[string][]uint64
	gen     uint64
	last    *Result
	lastGen uint64
}

// New 构造期数为 t（1 到 52）的计算器。
func New(t uint64) (*Engine, error) {
	if t < 1 || t > maxT {
		return nil, fmt.Errorf("mrp: New(T=%d): %w", t, ErrInvalid)
	}
	st := stock.New()
	return &Engine{
		t:      t,
		st:     st,
		bm:     bom.New(st),
		demand: make(map[string][]uint64),
		sched:  make(map[string][]uint64),
	}, nil
}

// AddItem 登记物料，见 stock.AddItem。
func (e *Engine) AddItem(id []byte, onHand, ss, lead, lotMin, lotMult uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.st.AddItem(id, onHand, ss, lead, lotMin, lotMult); err != nil {
		return err
	}
	e.gen++
	return nil
}

// AddComponent 声明父子件关系，见 bom.AddComponent。
func (e *Engine) AddComponent(parent, child []byte, per, scrap uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.bm.AddComponent(parent, child, per, scrap); err != nil {
		return err
	}
	e.gen++
	return nil
}

// Demand 累加独立需求。
func (e *Engine) Demand(id []byte, t, qty uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkQtyArgs(id, t, qty); err != nil {
		return err
	}
	e.acc(e.demand, id, t, qty)
	e.gen++
	return nil
}

// Scheduled 累加已下达的在途到货。
func (e *Engine) Scheduled(id []byte, t, qty uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkQtyArgs(id, t, qty); err != nil {
		return err
	}
	e.acc(e.sched, id, t, qty)
	e.gen++
	return nil
}

func (e *Engine) checkQtyArgs(id []byte, t, qty uint64) error {
	if !stock.ValidItemID(id) || t < 1 || t > e.t || qty < 1 || qty > maxQty {
		return fmt.Errorf("mrp: (%q,t=%d,qty=%d): %w", id, t, qty, ErrInvalid)
	}
	if !e.st.Has(id) {
		return fmt.Errorf("mrp: (%q): %w", id, ErrNotExist)
	}
	return nil
}

// acc 累加分期数量，饱和于 limit+1 以便 Run 统一报溢出而不回绕。
func (e *Engine) acc(m map[string][]uint64, id []byte, t, qty uint64) {
	key := string(id)
	s := m[key]
	if s == nil {
		s = make([]uint64, e.t+1)
		m[key] = s
	}
	v := s[t] + qty // s[t] <= limit+1 且 qty <= 1e9，不会回绕
	if v > limit {
		v = limit + 1
	}
	s[t] = v
}

// lotSize 按批量规则计算订货量：max(lotMin, ⌈net/lotMult⌉·lotMult)。
func lotSize(net, lotMin, lotMult uint64) uint64 {
	q := (net + lotMult - 1) / lotMult * lotMult
	if q < lotMin {
		q = lotMin
	}
	return q
}

// Run 按低层码升序处理全部物料，返回各物料各期的 G、Rec、Rel、A 与例外清单。
// 任一中间量超过 1e15 时报 ErrOverflow 且不返回部分结果；不改库存，重复调用结果相同。
func (e *Engine) Run() (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := e.st.Snapshot()
	comps := e.bm.Components()
	periods := int(e.t)

	childrenOf := make(map[string][]bom.Component)
	indeg := make(map[string]int, len(items))
	for id := range items {
		indeg[id] = 0
	}
	for _, c := range comps {
		childrenOf[c.Parent] = append(childrenOf[c.Parent], c)
		indeg[c.Child]++
	}
	// 低层码：从任一无父物料到该物料的最长路径边数（Kahn 拓扑松弛）。
	llc := make(map[string]uint64, len(items))
	queue := make([]string, 0, len(items))
	for id := range items {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range childrenOf[cur] {
			if llc[cur]+1 > llc[c.Child] {
				llc[c.Child] = llc[cur] + 1
			}
			indeg[c.Child]--
			if indeg[c.Child] == 0 {
				queue = append(queue, c.Child)
			}
		}
	}
	order := make([]string, 0, len(items))
	for id := range items {
		order = append(order, id)
	}
	sort.Slice(order, func(i, j int) bool {
		if llc[order[i]] != llc[order[j]] {
			return llc[order[i]] < llc[order[j]]
		}
		return order[i] < order[j]
	})

	res := &Result{
		T:           e.t,
		Order:       order,
		Items:       make(map[string]*ItemResult, len(items)),
		independent: make(map[string][]uint64, len(items)),
		pegs:        make(map[string][][]PegEntry, len(items)),
	}
	gross := make(map[string][]uint64, len(items))
	for id := range items {
		g := make([]uint64, periods+1)
		indep := make([]uint64, periods+1)
		if d := e.demand[id]; d != nil {
			copy(indep, d)
			copy(g, d)
		}
		gross[id] = g
		res.independent[id] = indep
		res.pegs[id] = make([][]PegEntry, periods+1)
	}
	for id, s := range e.sched {
		for tt := 1; tt <= periods; tt++ {
			if s[tt] > limit {
				return nil, fmt.Errorf("mrp: Run: %q t=%d: %w", id, tt, ErrOverflow)
			}
		}
	}

	for _, id := range order {
		it := items[id]
		g := gross[id]
		s := e.sched[id]
		rec := make([]uint64, periods+1)
		rel := make([]uint64, periods+1)
		a := make([]uint64, periods+1)
		a[0] = it.OnHand
		for tt := 1; tt <= periods; tt++ {
			if g[tt] > limit {
				return nil, fmt.Errorf("mrp: Run: %q t=%d: %w", id, tt, ErrOverflow)
			}
			var sched uint64
			if s != nil {
				sched = s[tt]
			}
			x := int64(a[tt-1]) + int64(sched) - int64(g[tt])
			if x < int64(it.SS) {
				net := uint64(int64(it.SS) - x)
				r := lotSize(net, it.LotMin, it.LotMult)
				if r > limit {
					return nil, fmt.Errorf("mrp: Run: %q t=%d: %w", id, tt, ErrOverflow)
				}
				rec[tt] = r
				a[tt] = uint64(x + int64(r))
			} else {
				a[tt] = uint64(x)
			}
			if a[tt] > limit {
				return nil, fmt.Errorf("mrp: Run: %q t=%d: %w", id, tt, ErrOverflow)
			}
			if rec[tt] > 0 {
				rt := int64(tt) - int64(it.Lead)
				if rt < 1 {
					res.Exceptions = append(res.Exceptions, Exception{Item: id, T: uint64(tt), Short: uint64(1 - rt)})
					rt = 1
				}
				rel[rt] += rec[tt]
				if rel[rt] > limit {
					return nil, fmt.Errorf("mrp: Run: %q t=%d: %w", id, tt, ErrOverflow)
				}
			}
		}
		res.visitedItems++
		res.Items[id] = &ItemResult{G: g, Rec: rec, Rel: rel, A: a}
		// 父项处理完毕，按每期投放总量对子项分别向上取整后累计贡献。
		for _, c := range childrenOf[id] {
			res.visitedEdges++
			denom := uint64(1000) - c.Scrap
			cg := gross[c.Child]
			pegs := res.pegs[c.Child]
			for tt := 1; tt <= periods; tt++ {
				if rel[tt] == 0 {
					continue
				}
				hi, lo := bits.Mul64(rel[tt], c.Per*1000)
				if hi != 0 {
					return nil, fmt.Errorf("mrp: Run: %q->%q t=%d: %w", id, c.Child, tt, ErrOverflow)
				}
				contrib := lo / denom
				if lo%denom != 0 {
					contrib++
				}
				if contrib > limit {
					return nil, fmt.Errorf("mrp: Run: %q->%q t=%d: %w", id, c.Child, tt, ErrOverflow)
				}
				cg[tt] += contrib
				if cg[tt] > limit {
					return nil, fmt.Errorf("mrp: Run: %q t=%d: %w", c.Child, tt, ErrOverflow)
				}
				pegs[tt] = append(pegs[tt], PegEntry{Parent: id, Qty: contrib})
			}
		}
	}
	for _, p := range res.pegs {
		for tt := range p {
			sort.Slice(p[tt], func(i, j int) bool { return p[tt][i].Parent < p[tt][j].Parent })
		}
	}
	sort.Slice(res.Exceptions, func(i, j int) bool {
		if res.Exceptions[i].Item != res.Exceptions[j].Item {
			return res.Exceptions[i].Item < res.Exceptions[j].Item
		}
		return res.Exceptions[i].T < res.Exceptions[j].T
	})
	e.last = res
	e.lastGen = e.gen
	return res, nil
}

// Peg 返回最近一次 Run 中该物料该期毛需求的来源。
// 拒绝次序：参数非法 > 物料不存在 > 结果过期。
func (e *Engine) Peg(id []byte, t uint64) (PegInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !stock.ValidItemID(id) || t < 1 || t > e.t {
		return PegInfo{}, fmt.Errorf("mrp: Peg(%q,t=%d): %w", id, t, ErrInvalid)
	}
	if !e.st.Has(id) {
		return PegInfo{}, fmt.Errorf("mrp: Peg(%q): %w", id, ErrNotExist)
	}
	if e.last == nil || e.lastGen != e.gen {
		return PegInfo{}, fmt.Errorf("mrp: Peg(%q): %w", id, ErrStale)
	}
	key := string(id)
	info := PegInfo{Independent: e.last.independent[key][t]}
	if ps := e.last.pegs[key][t]; len(ps) > 0 {
		info.Parents = make([]PegEntry, len(ps))
		copy(info.Parents, ps)
	}
	return info, nil
}
