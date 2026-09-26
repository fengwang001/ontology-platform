// Package api 对外提供弦图判定接口。依赖 mcs。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/mcs"
	"ontology/ug"
)

// 四类可判定哨兵错误（与 ug 同一组，可用 errors.Is 判定）。
var (
	ErrNonPositiveN   = ug.ErrNonPositiveN
	ErrNodeOutOfRange = ug.ErrNodeOutOfRange
	ErrSelfLoop       = ug.ErrSelfLoop
	ErrDuplicateEdge  = ug.ErrDuplicateEdge
)

// Engine 持有一张图与其最近一次 Compute 的结果，读方法可并发调用。
type Engine struct {
	mu       sync.RWMutex
	g        *ug.Graph
	peo      []int
	chordal  bool
	violator int
}

// New 建 n 个节点的图；n 非正返回 ErrNonPositiveN。
func New(n int) (*Engine, error) {
	g, err := ug.New(n)
	if err != nil {
		return nil, err
	}
	return &Engine{g: g, violator: -1}, nil
}

// AddEdge 登记无向边；被拒时图状态不变。
func (e *Engine) AddEdge(u, v int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.g.AddEdge(u, v)
}

// EdgeCount 返回已登记边数。
func (e *Engine) EdgeCount() int { e.mu.RLock(); defer e.mu.RUnlock(); return e.g.EdgeCount() }

// Compute 跑 MCS + 成团检查并缓存结果。
func (e *Engine) Compute() {
	e.mu.Lock()
	defer e.mu.Unlock()
	var s mcs.Solver
	r := s.Compute(e.g)
	e.peo, e.chordal, e.violator = r.PEO, r.Chordal, r.Violator
}

// PEO 返回完美消除序的副本。
func (e *Engine) PEO() []int { e.mu.RLock(); defer e.mu.RUnlock(); return append([]int(nil), e.peo...) }

// IsChordal 报告图是否弦图（以最近一次 Compute 为准）。
func (e *Engine) IsChordal() bool { e.mu.RLock(); defer e.mu.RUnlock(); return e.chordal }

// FirstViolator 返回 PEO 中最靠前的不成团节点；弦图为 -1。
func (e *Engine) FirstViolator() int { e.mu.RLock(); defer e.mu.RUnlock(); return e.violator }

// SelfCheck 对内置图核验四条不变量，全部通过返回 nil。
// 只用自建图，不触碰接收者状态，可并发调用。
func (e *Engine) SelfCheck() error {
	// 不变量 1+2：六边图 PEO 恰为 MCS 序之逆，且判为弦图。
	g6, err := build(6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}})
	if err != nil {
		return err
	}
	g6.Compute()
	if !slices.Equal(g6.PEO(), []int{5, 4, 3, 2, 1, 0}) {
		return fmt.Errorf("selfcheck: bad peo %v", g6.PEO())
	}
	if !g6.IsChordal() || g6.FirstViolator() != -1 {
		return errors.New("selfcheck: six-edge graph must be chordal")
	}
	// 不变量 2+3：C4 非弦图、首违规为 3，朴素逐对检查复核同一 PEO。
	c4, err := build(4, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}})
	if err != nil {
		return err
	}
	c4.Compute()
	if c4.IsChordal() || c4.FirstViolator() != 3 {
		return errors.New("selfcheck: C4 must be non-chordal with violator 3")
	}
	if naiveChordal(c4.g, c4.PEO()) != c4.FirstViolator() {
		return errors.New("selfcheck: naive recheck disagrees on C4")
	}
	// 不变量 4：四类拒绝互不相同且不留痕。
	if _, err = New(0); !errors.Is(err, ErrNonPositiveN) {
		return errors.New("selfcheck: n<=0 not rejected")
	}
	bad, _ := New(3)
	_ = bad.AddEdge(0, 1)
	before := bad.EdgeCount()
	ops := []error{bad.AddEdge(1, 1), bad.AddEdge(0, 3), bad.AddEdge(1, 0)}
	wantErrs := []error{ErrSelfLoop, ErrNodeOutOfRange, ErrDuplicateEdge}
	for i := range ops {
		if !errors.Is(ops[i], wantErrs[i]) {
			return fmt.Errorf("selfcheck: op %d wrong error %v", i, ops[i])
		}
	}
	if bad.EdgeCount() != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}

func build(n int, edges [][2]int) (*Engine, error) {
	e, err := New(n)
	if err != nil {
		return nil, err
	}
	for _, uv := range edges {
		if err := e.AddEdge(uv[0], uv[1]); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// naiveChordal 朴素参照：按 PEO 逐节点对 N+(v) 两两查边，返回首个违规节点。
func naiveChordal(g *ug.Graph, peo []int) int {
	rank := make([]int, len(peo))
	for i, v := range peo {
		rank[v] = i
	}
	for _, v := range peo {
		var later []int
		for _, u := range g.Neighbors(v) {
			if rank[u] > rank[v] {
				later = append(later, u)
			}
		}
		for i := 0; i < len(later); i++ {
			for j := i + 1; j < len(later); j++ {
				if !g.HasEdge(later[i], later[j]) {
					return v
				}
			}
		}
	}
	return -1
}
