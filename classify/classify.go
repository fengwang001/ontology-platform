// Package classify 在 catalog（列登记与人工定级）与 lineage（派生边）
// 之上提供有效级别的增量传播与影响查询。所有公共操作互斥串行，
// 并发调用等价于某个串行顺序；相同操作序列重放得到相同的 Changed 序列。
package classify

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/catalog"
	"ontology/lineage"
)

// 拒绝原因，可用 errors.Is 区分。检查次序：参数非法 > 时钟回退 >
// 列不存在 > 已存在/边不存在 > 超限 > 成环，只报第一个。
var (
	ErrInvalid      = errors.New("classify: invalid argument")
	ErrClock        = errors.New("classify: clock regression")
	ErrNotFound     = errors.New("classify: column not found")
	ErrExists       = errors.New("classify: column or edge already exists")
	ErrEdgeNotFound = errors.New("classify: edge not found")
	ErrLimit        = errors.New("classify: limit exceeded")
	ErrCycle        = errors.New("classify: cycle detected")
)

const maxNow = int64(1_000_000_000_000)

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// Change 记录一列有效级别在一次操作前后的变化。
type Change struct {
	Col      string
	Old, New int
}

// ReasonKind 是 Why 返回的决定来源类别。
type ReasonKind int

const (
	ReasonFloor ReasonKind = iota // 下限 floor 压过 min(cap, raw)
	ReasonCap                     // 生效的上限 cap 低于 raw
	ReasonBase                    // 固有级别 base 决定
	ReasonEdge                    // 某条入边决定，Src 给出上游列
)

// Reason 是 Why 的返回值；Kind 为 ReasonEdge 时 Src 为贡献最大的
// 入边中名字字节序最小的上游列。
type Reason struct {
	Kind ReasonKind
	Src  string
}

// Engine 是敏感级别传播与影响分析引擎。
type Engine struct {
	mu    sync.Mutex
	cat   *catalog.Catalog
	g     *lineage.Graph
	now   int64
	eff   map[string]int
	evals int // 最近一次被接受操作中重新求值 eff 的列次数
}

// New 创建列数上限为 nmax（1 到 1e5）的引擎。
func New(nmax int) (*Engine, error) {
	if nmax < 1 || nmax > 100_000 {
		return nil, fmt.Errorf("%w: nmax %d out of [1, 1e5]", ErrInvalid, nmax)
	}
	return &Engine{
		cat: catalog.New(nmax),
		g:   lineage.New(),
		eff: make(map[string]int),
	}, nil
}

// raw 计算 max(base, 各入边 f(eff(src)))。
func (e *Engine) raw(name string) int {
	r := e.cat.Get(name).Base
	for src, k := range e.g.InEdges(name) {
		if v := k.Map(e.eff[src]); v > r {
			r = v
		}
	}
	return r
}

// computeEff 按 eff = max(floor, min(cap, raw)) 计算有效级别。
// 不变式：CapSet 的列其 until 一定大于 e.now（每次接受操作时惰性失效）。
func (e *Engine) computeEff(name string) int {
	col := e.cat.Get(name)
	eff := e.raw(name)
	if e.cat.CapActive(name, e.now) && col.Cap < eff {
		eff = col.Cap
	}
	if col.Floor > eff {
		eff = col.Floor
	}
	return eff
}

// propagate 从种子列（被直接改动的列 + 本次失效的上限列）出发，
// 在受影响子图上按 Kahn 拓扑序重算 eff：每列至多求值一次，
// 仅当某入邻居 eff 变化才求值（未变即止步）。
// 返回按列名字节序升序的 Changed。dryRun 时不落盘任何状态（WhatIf 用）。
func (e *Engine) propagate(seeds []string, dryRun bool) []Change {
	savedEvals := e.evals
	e.evals = 0

	seedSet := make(map[string]bool, len(seeds))
	affected := make(map[string]bool)
	queue := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if !seedSet[s] {
			seedSet[s] = true
			affected[s] = true
			queue = append(queue, s)
		}
	}
	// 种子可达集（遍历不计 evals）。
	for i := 0; i < len(queue); i++ {
		for dst := range e.g.OutEdges(queue[i]) {
			if !affected[dst] {
				affected[dst] = true
				queue = append(queue, dst)
			}
		}
	}
	if len(affected) == 0 {
		return nil
	}

	// 子图内入度与"已变化入邻居数"。
	indeg := make(map[string]int, len(affected))
	for v := range affected {
		n := 0
		for src := range e.g.InEdges(v) {
			if affected[src] {
				n++
			}
		}
		indeg[v] = n
	}
	changedIn := make(map[string]int)
	changed := make(map[string]bool)
	var changes []Change
	oldEff := make(map[string]int) // dryRun 回滚用

	var ready []string
	for v := range affected {
		if indeg[v] == 0 {
			ready = append(ready, v)
		}
	}
	for len(ready) > 0 {
		u := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		if seedSet[u] || changedIn[u] > 0 {
			e.evals++
			old := e.eff[u]
			if nw := e.computeEff(u); nw != old {
				if dryRun {
					if _, ok := oldEff[u]; !ok {
						oldEff[u] = old
					}
				}
				e.eff[u] = nw
				changed[u] = true
				changes = append(changes, Change{Col: u, Old: old, New: nw})
			}
		}
		for dst := range e.g.OutEdges(u) {
			if changed[u] {
				changedIn[dst]++
			}
			indeg[dst]--
			if indeg[dst] == 0 {
				ready = append(ready, dst)
			}
		}
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Col < changes[j].Col })
	if dryRun {
		for name, old := range oldEff {
			e.eff[name] = old
		}
		e.evals = savedEvals
	}
	return changes
}

// accept 在被接受操作的公共尾部：推进时钟、失效上限、返回传播种子前缀。
func (e *Engine) accept(now int64) []string {
	e.now = now
	return e.cat.ExpireCaps(now)
}

func (e *Engine) checkClock(now int64) error {
	if now < e.now {
		return fmt.Errorf("%w: now=%d < accepted max %d", ErrClock, now, e.now)
	}
	return nil
}

// AddColumn 登记列及其固有级别。新列自身不入 Changed。
func (e *Engine) AddColumn(name string, base int, now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) || !catalog.ValidLevel(base) || !validNow(now) {
		return nil, fmt.Errorf("%w: AddColumn(%q, %d, %d)", ErrInvalid, name, base, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	if e.cat.Has(name) {
		return nil, fmt.Errorf("%w: column %q", ErrExists, name)
	}
	if e.cat.Len() >= e.cat.NMax() {
		return nil, fmt.Errorf("%w: Nmax %d", ErrLimit, e.cat.NMax())
	}
	seeds := e.accept(now)
	changes := e.propagate(seeds, false)
	e.cat.Add(name, base)
	e.evals++
	e.eff[name] = e.computeEff(name)
	return changes, nil
}

// SetBase 修改固有级别。
func (e *Engine) SetBase(name string, base int, now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) || !catalog.ValidLevel(base) || !validNow(now) {
		return nil, fmt.Errorf("%w: SetBase(%q, %d, %d)", ErrInvalid, name, base, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	if !e.cat.Has(name) {
		return nil, fmt.Errorf("%w: column %q", ErrNotFound, name)
	}
	seeds := append(e.accept(now), name)
	e.cat.SetBase(name, base)
	return e.propagate(seeds, false), nil
}

// AddEdge 登记 src->dst 派生边。
func (e *Engine) AddEdge(src, dst string, kind lineage.Kind, now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(src) || !catalog.ValidName(dst) || !lineage.ValidKind(kind) || src == dst || !validNow(now) {
		return nil, fmt.Errorf("%w: AddEdge(%q, %q, %d, %d)", ErrInvalid, src, dst, kind, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	for _, c := range [2]string{src, dst} {
		if !e.cat.Has(c) {
			return nil, fmt.Errorf("%w: column %q", ErrNotFound, c)
		}
	}
	if e.g.HasEdge(src, dst) {
		return nil, fmt.Errorf("%w: edge %q -> %q", ErrExists, src, dst)
	}
	if e.g.InDegree(dst) >= lineage.MaxInDegree {
		return nil, fmt.Errorf("%w: in-degree of %q exceeds %d", ErrLimit, dst, lineage.MaxInDegree)
	}
	if e.g.Reachable(dst, src) {
		return nil, fmt.Errorf("%w: edge %q -> %q closes a cycle", ErrCycle, src, dst)
	}
	seeds := append(e.accept(now), dst)
	e.g.AddEdge(src, dst, kind)
	return e.propagate(seeds, false), nil
}

// RemoveEdge 删除 src->dst 派生边。
func (e *Engine) RemoveEdge(src, dst string, now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(src) || !catalog.ValidName(dst) || src == dst || !validNow(now) {
		return nil, fmt.Errorf("%w: RemoveEdge(%q, %q, %d)", ErrInvalid, src, dst, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	for _, c := range [2]string{src, dst} {
		if !e.cat.Has(c) {
			return nil, fmt.Errorf("%w: column %q", ErrNotFound, c)
		}
	}
	if !e.g.HasEdge(src, dst) {
		return nil, fmt.Errorf("%w: edge %q -> %q", ErrEdgeNotFound, src, dst)
	}
	seeds := append(e.accept(now), dst)
	e.g.RemoveEdge(src, dst)
	return e.propagate(seeds, false), nil
}

// Raise 设下限 floor=L，L 为 0 即取消，重复调用覆盖。
func (e *Engine) Raise(name string, l int, now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) || !catalog.ValidLevel(l) || !validNow(now) {
		return nil, fmt.Errorf("%w: Raise(%q, %d, %d)", ErrInvalid, name, l, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	if !e.cat.Has(name) {
		return nil, fmt.Errorf("%w: column %q", ErrNotFound, name)
	}
	seeds := append(e.accept(now), name)
	e.cat.SetFloor(name, l)
	return e.propagate(seeds, false), nil
}

// Declass 设经审批的临时上限 cap=L，要求 until 严格大于 now。
func (e *Engine) Declass(name string, l int, until, now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) || !catalog.ValidLevel(l) || !validNow(now) || until <= now {
		return nil, fmt.Errorf("%w: Declass(%q, %d, %d, %d)", ErrInvalid, name, l, until, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	if !e.cat.Has(name) {
		return nil, fmt.Errorf("%w: column %q", ErrNotFound, name)
	}
	seeds := append(e.accept(now), name)
	e.cat.SetCap(name, l, until)
	return e.propagate(seeds, false), nil
}

// Tick 推进时钟并让到期上限失效。
func (e *Engine) Tick(now int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validNow(now) {
		return nil, fmt.Errorf("%w: Tick(%d)", ErrInvalid, now)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	return e.propagate(e.accept(now), false), nil
}

// Eff 返回列当前有效级别（只读，不推进时钟、不失效上限）。
func (e *Engine) Eff(name string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) {
		return 0, fmt.Errorf("%w: Eff(%q)", ErrInvalid, name)
	}
	if !e.cat.Has(name) {
		return 0, fmt.Errorf("%w: column %q", ErrNotFound, name)
	}
	return e.eff[name], nil
}

// Why 返回决定 eff 的唯一来源，按 Floor > Cap > Base > Edge 次序取第一个成立者。
func (e *Engine) Why(name string) (Reason, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) {
		return Reason{}, fmt.Errorf("%w: Why(%q)", ErrInvalid, name)
	}
	if !e.cat.Has(name) {
		return Reason{}, fmt.Errorf("%w: column %q", ErrNotFound, name)
	}
	col := e.cat.Get(name)
	raw := e.raw(name)
	minCapRaw := raw
	capActive := e.cat.CapActive(name, e.now)
	if capActive && col.Cap < minCapRaw {
		minCapRaw = col.Cap
	}
	if col.Floor > minCapRaw {
		return Reason{Kind: ReasonFloor}, nil
	}
	if capActive && col.Cap < raw {
		return Reason{Kind: ReasonCap}, nil
	}
	baseWins := true // base 不小于所有入边贡献（含无入边）
	best, bestSrc := -1, ""
	for src, k := range e.g.InEdges(name) {
		v := k.Map(e.eff[src])
		if v > col.Base {
			baseWins = false
		}
		if v > best || (v == best && src < bestSrc) {
			best, bestSrc = v, src
		}
	}
	if baseWins {
		return Reason{Kind: ReasonBase}, nil
	}
	return Reason{Kind: ReasonEdge, Src: bestSrc}, nil
}

// WhatIf 只读模拟把该列固有级别改为 base 会产生的 Changed，不改任何状态。
func (e *Engine) WhatIf(name string, base int) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !catalog.ValidName(name) || !catalog.ValidLevel(base) {
		return nil, fmt.Errorf("%w: WhatIf(%q, %d)", ErrInvalid, name, base)
	}
	if !e.cat.Has(name) {
		return nil, fmt.Errorf("%w: column %q", ErrNotFound, name)
	}
	saved := e.cat.Get(name).Base
	e.cat.SetBase(name, base)
	changes := e.propagate([]string{name}, true)
	e.cat.SetBase(name, saved)
	return changes, nil
}
