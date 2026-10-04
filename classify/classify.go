// Package classify 在 catalog 与 lineage 之上做有效级别的增量传播与影响分析。
package classify

import (
	"errors"
	"sort"
	"sync"

	"ontology/catalog"
	"ontology/lineage"
)

// 可被 errors.Is 区分的拒绝原因。
var (
	ErrInvalid   = errors.New("classify: invalid argument")
	ErrClock     = errors.New("classify: clock moved backwards")
	ErrNoColumn  = errors.New("classify: column does not exist")
	ErrDuplicate = errors.New("classify: column or edge already exists")
	ErrNoEdge    = errors.New("classify: edge does not exist")
	ErrLimit     = errors.New("classify: limit exceeded")
	ErrCycle     = errors.New("classify: edge would create a cycle")
)

// Kind 重导出 lineage 的四种边类型，便于调用方只依赖本包。
type Kind = lineage.Kind

// 四种派生关系。
const (
	Copy = lineage.Copy
	Mask = lineage.Mask
	Hash = lineage.Hash
	Agg  = lineage.Agg
)

// ChangedItem 是一列 eff 在一次操作前后的取值。
type ChangedItem struct {
	Col string
	Old int
	New int
}

// WhySource 标识决定 eff 的唯一来源。
type WhySource struct {
	Kind string // "Floor" | "Cap" | "Base" | "Edge"
	Src  string // Kind=="Edge" 时的上游列名
}

const (
	maxNameLen = 128
	minLevel   = 0
	maxLevel   = 4
	maxInEdges = 16
)

// Engine 是传播与影响分析引擎；所有方法可并发调用。
type Engine struct {
	mu      sync.Mutex
	cat     *catalog.Catalog
	graph   *lineage.Graph
	lastNow int64
	eff     map[string]int
	evals   int64
}

// NewEngine 创建列数上限为 nmax 的引擎。
func NewEngine(nmax int) *Engine {
	if nmax < 1 || nmax > 100000 {
		panic("classify: nmax out of range")
	}
	return &Engine{
		cat:   catalog.New(nmax),
		graph: lineage.NewGraph(),
		eff:   make(map[string]int),
	}
}

func validName(col string) bool { return len(col) >= 1 && len(col) <= maxNameLen }

func validLevel(level int) bool { return level >= minLevel && level <= maxLevel }

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

// AddColumn 登记列及其固有级别。
func (e *Engine) AddColumn(col string, base int, now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) || !validLevel(base) || !validNow(now) {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	if e.cat.Has(col) {
		return nil, ErrDuplicate
	}
	if e.cat.Len() >= e.cat.Nmax() {
		return nil, ErrLimit
	}
	// 被接受：先惰性落地上限失效（新列无 cap，不受影响），再登记。
	expired := e.cat.ExpireCaps(now)
	e.cat.Add(col, base)
	e.lastNow = now
	// 新列求值一次，但不入 Changed；新列此刻不可能有任何边。
	e.evals++
	e.eff[col] = e.computeEff(col, e.eff)
	// 同批失效 cap 的其他列仍需正常传播。
	return e.recompute(expired, e.eff, true), nil
}

// SetBase 修改固有级别。
func (e *Engine) SetBase(col string, base int, now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) || !validLevel(base) || !validNow(now) {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	if !e.cat.Has(col) {
		return nil, ErrNoColumn
	}
	expired := e.cat.ExpireCaps(now)
	e.cat.SetBase(col, base)
	e.lastNow = now
	return e.recompute(append(expired, col), e.eff, true), nil
}

// AddEdge 登记派生边。
func (e *Engine) AddEdge(src, dst string, kind Kind, now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(src) || !validName(dst) || !lineage.ValidKind(kind) || !validNow(now) || src == dst {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	if !e.cat.Has(src) || !e.cat.Has(dst) {
		return nil, ErrNoColumn
	}
	if e.graph.HasEdge(src, dst) {
		return nil, ErrDuplicate
	}
	if e.graph.InDegree(dst) >= maxInEdges {
		return nil, ErrLimit
	}
	if e.graph.Reaches(dst, src) {
		return nil, ErrCycle
	}
	expired := e.cat.ExpireCaps(now)
	e.graph.Add(src, dst, kind)
	e.lastNow = now
	return e.recompute(append(expired, dst), e.eff, true), nil
}

// RemoveEdge 删除派生边。
func (e *Engine) RemoveEdge(src, dst string, now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(src) || !validName(dst) || !validNow(now) || src == dst {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	if !e.cat.Has(src) || !e.cat.Has(dst) {
		return nil, ErrNoColumn
	}
	if !e.graph.HasEdge(src, dst) {
		return nil, ErrNoEdge
	}
	expired := e.cat.ExpireCaps(now)
	e.graph.Remove(src, dst)
	e.lastNow = now
	return e.recompute(append(expired, dst), e.eff, true), nil
}

// Raise 设置下限（L=0 取消）。
func (e *Engine) Raise(col string, level int, now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) || !validLevel(level) || !validNow(now) {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	if !e.cat.Has(col) {
		return nil, ErrNoColumn
	}
	expired := e.cat.ExpireCaps(now)
	e.cat.SetFloor(col, level)
	e.lastNow = now
	return e.recompute(append(expired, col), e.eff, true), nil
}

// Declass 设置临时上限。
func (e *Engine) Declass(col string, level int, until, now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) || !validLevel(level) || !validNow(now) || !validNow(until) || until <= now {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	if !e.cat.Has(col) {
		return nil, ErrNoColumn
	}
	expired := e.cat.ExpireCaps(now)
	e.cat.SetCap(col, &catalog.Cap{Level: level, Until: until})
	e.lastNow = now
	return e.recompute(append(expired, col), e.eff, true), nil
}

// Tick 仅推进时间并惰性落地上限失效。
func (e *Engine) Tick(now int64) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validNow(now) {
		return nil, ErrInvalid
	}
	if now < e.lastNow {
		return nil, ErrClock
	}
	expired := e.cat.ExpireCaps(now)
	e.lastNow = now
	if len(expired) == 0 {
		return nil, nil
	}
	return e.recompute(expired, e.eff, true), nil
}

// Eff 返回当前有效级别。
func (e *Engine) Eff(col string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) {
		return 0, ErrInvalid
	}
	if !e.cat.Has(col) {
		return 0, ErrNoColumn
	}
	return e.eff[col], nil
}

// Why 返回决定 eff 的唯一来源。
func (e *Engine) Why(col string) (WhySource, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) {
		return WhySource{}, ErrInvalid
	}
	colRec := e.cat.Get(col)
	if colRec == nil {
		return WhySource{}, ErrNoColumn
	}
	raw := colRec.Base
	bestSrc, bestContrib := "", -1
	for _, ed := range e.graph.InEdges(col) {
		contrib := ed.Kind.Apply(e.eff[ed.Src])
		if contrib > bestContrib {
			bestContrib, bestSrc = contrib, ed.Src
		}
		if contrib > raw {
			raw = contrib
		}
	}
	capped := raw
	if colRec.Cap != nil && colRec.Cap.Level < capped {
		capped = colRec.Cap.Level
	}
	switch {
	case colRec.Floor > capped:
		return WhySource{Kind: "Floor"}, nil
	case colRec.Cap != nil && colRec.Cap.Level < raw:
		return WhySource{Kind: "Cap"}, nil
	case colRec.Base >= bestContrib:
		return WhySource{Kind: "Base"}, nil
	default:
		return WhySource{Kind: "Edge", Src: bestSrc}, nil
	}
}

// WhatIf 只读模拟修改固有级别的影响。
func (e *Engine) WhatIf(col string, base int) ([]ChangedItem, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validName(col) || !validLevel(base) {
		return nil, ErrInvalid
	}
	colRec := e.cat.Get(col)
	if colRec == nil {
		return nil, ErrNoColumn
	}
	snapshot := colRec.Base
	working := make(map[string]int, len(e.eff))
	for name, level := range e.eff {
		working[name] = level
	}
	colRec.Base = base
	changed := e.recompute([]string{col}, working, false)
	colRec.Base = snapshot
	return changed, nil
}

// Evals 返回累计的 eff 求值列次（测试用）。
func (e *Engine) Evals() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evals
}

// computeEff 依据当前 catalog、graph 与给定 eff 表计算一列的有效级别。
func (e *Engine) computeEff(col string, effs map[string]int) int {
	colRec := e.cat.Get(col)
	raw := colRec.Base
	for _, ed := range e.graph.InEdges(col) {
		if contrib := ed.Kind.Apply(effs[ed.Src]); contrib > raw {
			raw = contrib
		}
	}
	eff := raw
	if colRec.Cap != nil && colRec.Cap.Level < eff {
		eff = colRec.Cap.Level
	}
	if colRec.Floor > eff {
		eff = colRec.Floor
	}
	return eff
}

// recompute 以 seeds 为直接改动列，按受影响子图的拓扑序增量重算 eff。
// 每个种子必求值；其余列仅当受影响子图内某个上游 eff 变化才求值；每列至多一次。
// count 为 true 时把求值列次计入 e.evals（WhatIf 只读，不计数）。
func (e *Engine) recompute(seedList []string, effs map[string]int, count bool) []ChangedItem {
	seeds := dedup(seedList)
	affected := make(map[string]bool, len(seeds))
	stack := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if affected[seed] {
			continue
		}
		affected[seed] = true
		stack = append(stack, seed)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range e.graph.OutNeighbors(cur) {
			if !affected[next] {
				affected[next] = true
				stack = append(stack, next)
			}
		}
	}

	indeg := make(map[string]int, len(affected))
	for node := range affected {
		for _, ed := range e.graph.InEdges(node) {
			if affected[ed.Src] {
				indeg[node]++
			}
		}
	}
	force := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		force[seed] = true
	}
	marked := make(map[string]bool)

	nodes := make([]string, 0, len(affected))
	for node := range affected {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	queue := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if indeg[node] == 0 {
			queue = append(queue, node)
		}
	}

	evaluated := 0
	var changed []ChangedItem
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if force[cur] || marked[cur] {
			evaluated++
			old := effs[cur]
			next := e.computeEff(cur, effs)
			effs[cur] = next
			if old != next {
				changed = append(changed, ChangedItem{Col: cur, Old: old, New: next})
				for _, down := range e.graph.OutNeighbors(cur) {
					if affected[down] {
						marked[down] = true
					}
				}
			}
		}
		for _, down := range e.graph.OutNeighbors(cur) {
			if affected[down] {
				indeg[down]--
				if indeg[down] == 0 {
					queue = append(queue, down)
				}
			}
		}
	}
	if count {
		e.evals += int64(evaluated)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Col < changed[j].Col })
	return changed
}

func dedup(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := items[:0]
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
