// Package api 是增量传递闭包对外接口：带重数增删边、可达查询、有序可达对导出与自检。仅依赖 closure；方法并发安全。
package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ontology/closure"
	"ontology/graph"
)

// 四个哨兵错误可判定且互不相同。
var ErrInvalidMaxEdges, ErrEmptyNode, ErrEdgeNotExist, ErrTooManyEdges = errors.New("maxEdges must be positive"), errors.New("node name must not be empty"), errors.New("edge does not exist"), errors.New("number of distinct edges exceeds maxEdges")

// Engine 是进程内的增量传递闭包引擎。
type Engine struct {
	mu  sync.RWMutex
	max int
	c   *closure.Closure
}

// New 创建不同存在边条数上限为 maxEdges 的引擎；非正返回 ErrInvalidMaxEdges。
func New(maxEdges int) (*Engine, error) {
	if maxEdges <= 0 {
		return nil, ErrInvalidMaxEdges
	}
	return &Engine{max: maxEdges, c: closure.New()}, nil
}

// AddEdge 使 (u,v) 重数加 1；空节点 ErrEmptyNode，新增会超限 ErrTooManyEdges。
func (e *Engine) AddEdge(u, v string) error {
	if u == "" || v == "" {
		return ErrEmptyNode
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.c.HasEdge(u, v) && e.c.EdgeCount() >= e.max {
		return ErrTooManyEdges
	}
	e.c.AddEdge(u, v)
	return nil
}

// RemoveEdge 使 (u,v) 重数减 1，返回过删数与再推导数；空节点 ErrEmptyNode，不存在 ErrEdgeNotExist。
func (e *Engine) RemoveEdge(u, v string) (overDeleted, rederived int, err error) {
	if u == "" || v == "" {
		return 0, 0, ErrEmptyNode
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.c.HasEdge(u, v) {
		return 0, 0, ErrEdgeNotExist
	}
	overDeleted, rederived, _ = e.c.RemoveEdge(u, v)
	return overDeleted, rederived, nil
}

// Reachable 报告是否有长度 ≥1 的路径从 x 到 y。Pairs 返回按 (x,y) 字典序的全部可达对副本。
func (e *Engine) Reachable(x, y string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c.Reachable(x, y)
}

func (e *Engine) Pairs() [][2]string {
	e.mu.RLock()
	ps := e.c.Pairs()
	e.mu.RUnlock()
	out := make([][2]string, len(ps))
	for i, p := range ps {
		out[i] = [2]string{p.U, p.V}
	}
	return out
}

// SelfCheck 用独立临时引擎对内置序列核验四条不变量，不影响接收者状态。
func (e *Engine) SelfCheck() error {
	eng, _ := New(50)
	ref := graph.New()
	prev := 0
	ops := "a A B,a B C,a A C,a C A,a B C,r B C,r A B,a C D,r C A,r B C,a x x,a x x,r x x"
	for i, t := range strings.Split(ops, ",") {
		f := strings.Fields(t)
		op, u, v := f[0], f[1], f[2]
		od, rd, err := 0, 0, error(nil)
		if op == "a" {
			err, _ = eng.AddEdge(u, v), ref.Add(u, v)
		} else {
			od, rd, err = eng.RemoveEdge(u, v)
			ref.Remove(u, v)
		}
		if err != nil {
			return err
		}
		got := eng.Pairs()
		if !slices.Equal(got, ref.NaivePairs()) { // 不变量1、2：与朴素BFS逐对一致
			return fmt.Errorf("step %d: R != naive BFS", i+1)
		}
		bad3 := op == "a" && len(got) < prev || op == "r" && (len(got) > prev || prev-len(got) != od-rd)
		if bad3 { // 不变量3：加不缩、删不增、减量=OD-RD
			return fmt.Errorf("step %d: monotonicity or delta violated", i+1)
		}
		prev = len(got)
	}
	_ = eng.AddEdge("p", "q")
	_ = eng.AddEdge("p", "q")
	before := eng.Pairs()
	_, _, err := eng.RemoveEdge("p", "q")
	if err != nil || !eng.Reachable("p", "q") || !slices.Equal(eng.Pairs(), before) { // 不变量2
		return errors.New("multiplicity 2->1 changed R")
	}
	return rejectLeavesNoTrace() // 不变量4
}

// rejectLeavesNoTrace 核验四类拒绝互不相同、不留痕、之后引擎仍可用。
func rejectLeavesNoTrace() error {
	if _, err := New(0); err != ErrInvalidMaxEdges {
		return fmt.Errorf("New(0): %w", err)
	}
	eng, _ := New(1)
	before := eng.Pairs()
	rm := func(u, v string) error { _, _, e := eng.RemoveEdge(u, v); return e }
	cases := []struct {
		want error
		got  error
	}{
		{ErrEmptyNode, eng.AddEdge("", "z")},
		{ErrEmptyNode, rm("a", "")},
		{ErrEdgeNotExist, rm("a", "b")},
	}
	for _, c := range cases {
		if c.got != c.want || !slices.Equal(eng.Pairs(), before) {
			return fmt.Errorf("reject %v wrong or left trace", c.want)
		}
	}
	if err := eng.AddEdge("a", "b"); err != nil {
		return err
	}
	before = eng.Pairs()
	if err := eng.AddEdge("c", "d"); err != ErrTooManyEdges || !slices.Equal(eng.Pairs(), before) {
		return errors.New("ErrTooManyEdges reject wrong or left trace")
	}
	if _, _, err := eng.RemoveEdge("a", "b"); err != nil || eng.Reachable("a", "b") {
		return errors.New("engine unusable after rejections")
	}
	return nil
}
