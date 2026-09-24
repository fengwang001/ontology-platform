// Package api 是 DRed 增量传递闭包的对外门面：参数校验、哨兵错误、并发锁。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/closure"
	"ontology/graph"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrBadMaxEdges  = errors.New("api: maxEdges 必须为正整数")
	ErrEmptyNode    = errors.New("api: 节点名不能为空串")
	ErrEdgeNotFound = errors.New("api: 要删除的边不存在")
	ErrTooManyEdges = errors.New("api: 存在边数将超过 maxEdges")
)

// Engine 维护带重数边集与可达对集合 R。所有方法可并发调用。
type Engine struct {
	mu  sync.RWMutex
	max int
	g   *graph.Graph
	cl  *closure.Closure
}

// New 创建引擎；maxEdges 非正时返回 ErrBadMaxEdges。
func New(maxEdges int) (*Engine, error) {
	if maxEdges <= 0 {
		return nil, ErrBadMaxEdges
	}
	g := graph.New()
	return &Engine{max: maxEdges, g: g, cl: closure.New(g)}, nil
}

// AddEdge 使 (u,v) 重数加 1；边由不存在变为存在时增量并入 R。
func (e *Engine) AddEdge(u, v string) error {
	if u == "" || v == "" {
		return ErrEmptyNode
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.g.Has(u, v) && e.g.Edges() >= e.max {
		return ErrTooManyEdges
	}
	if e.g.Add(u, v) {
		e.cl.AddEdge(u, v)
	}
	return nil
}

// RemoveEdge 使 (u,v) 重数减 1；边消失时执行 DRed，返回过删数与再推导数。
func (e *Engine) RemoveEdge(u, v string) (int, int, error) {
	if u == "" || v == "" {
		return 0, 0, ErrEmptyNode
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.g.Has(u, v) {
		return 0, 0, ErrEdgeNotFound
	}
	if e.g.Remove(u, v) {
		od, re := e.cl.RemoveEdge(u, v)
		return od, re, nil
	}
	return 0, 0, nil
}

// Reachable 报告 (x,y) 是否在 R 中。
func (e *Engine) Reachable(x, y string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cl.Reachable(x, y)
}

// Pairs 返回 R 中全部可达对，按 (x,y) 字典序排列。
func (e *Engine) Pairs() [][2]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cl.Pairs()
}

// SelfCheck 在独立实例上跑内置增删序列，核验四条不变量；不碰接收者状态，可并发调用。
func (e *Engine) SelfCheck() error {
	eng, err := New(64)
	if err != nil {
		return err
	}
	type op struct {
		add  bool
		u, v string
	}
	ops := []op{{true, "a", "b"}, {true, "b", "c"}, {true, "c", "a"}, {true, "a", "c"}, {true, "a", "c"},
		{false, "a", "c"}, {false, "b", "c"}, {false, "c", "a"}, {false, "a", "b"}}
	mult := make(map[[2]string]int)
	prev := 0
	for i, o := range ops {
		if o.add {
			err = eng.AddEdge(o.u, o.v)
			mult[[2]string{o.u, o.v}]++
		} else {
			_, _, err = eng.RemoveEdge(o.u, o.v)
			mult[[2]string{o.u, o.v}]--
		}
		if err != nil {
			return fmt.Errorf("selfcheck 步%d: %w", i, err)
		}
		if got := len(eng.Pairs()); (o.add && got < prev) || (!o.add && got > prev) {
			return fmt.Errorf("selfcheck 步%d: 单调性被破坏", i)
		}
		prev = len(eng.Pairs())
		if err := checkNaive(eng, mult); err != nil {
			return fmt.Errorf("selfcheck 步%d: %w", i, err)
		}
	}
	before := eng.Pairs()
	if _, _, err := eng.RemoveEdge("x", "y"); !errors.Is(err, ErrEdgeNotFound) {
		return errors.New("selfcheck: 删不存在的边未报 ErrEdgeNotFound")
	}
	if !reflect.DeepEqual(eng.Pairs(), before) {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}

// checkNaive 核验 eng 的 R 与按 mult 中存在的边集朴素 BFS 的结果逐对相同。
func checkNaive(eng *Engine, mult map[[2]string]int) error {
	g := graph.New()
	for e, m := range mult {
		for i := 0; i < m; i++ {
			g.Add(e[0], e[1])
		}
	}
	want, got := closure.Naive(g), eng.Pairs()
	if len(got) != len(want) {
		return fmt.Errorf("|R|=%d, 朴素参照=%d", len(got), len(want))
	}
	for _, p := range got {
		if !want[p] {
			return fmt.Errorf("多出可达对 %v", p)
		}
	}
	return nil
}
