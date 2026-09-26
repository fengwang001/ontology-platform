// Package api 是对外门面：建图、加边、计算支配树、回答支配查询。
package api

import (
	"errors"
	"fmt"

	"ontology/dgraph"
	"ontology/dom"
)

// Engine 持有一张图与其支配计算结果。
type Engine struct {
	g *dgraph.Graph
	d *dom.Dom
}

// New 建 n 个节点的空图；n 非正返回可判定错误。
func New(n int) (*Engine, error) {
	g, err := dgraph.New(n)
	if err != nil {
		return nil, err
	}
	return &Engine{g: g}, nil
}

func (e *Engine) AddEdge(u, v int) error { return e.g.AddEdge(u, v) } // 失败不改变图状态
func (e *Engine) Compute()               { e.d = dom.Compute(e.g) }   // 之后查询方法可并发调用
func (e *Engine) EdgeCount() int         { return e.g.Edges() }

// IDom 返回 v 的直接支配者；不可达返回 -1；越界返回可判定错误。
func (e *Engine) IDom(v int) (int, error) {
	if v < 0 || v >= e.g.N() {
		return 0, dgraph.ErrNodeRange
	}
	return e.d.IDom(v), nil
}

// Dominates 报告 a 是否支配 b；b 不可达恒 false。
func (e *Engine) Dominates(a, b int) bool { return e.d.Dominates(a, b) }

// SelfCheck 对一组内置图核验四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	ess := [][][2]int{
		{{0, 1}, {0, 6}, {1, 2}, {1, 6}, {2, 3}, {2, 4}, {3, 5}, {4, 5}}, // 第三节的图
		{{0, 1}, {1, 2}, {2, 1}, {2, 3}},                                 // 环 + 不可达节点 4
		{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {3, 4}, {4, 5}},                 // 菱形 + 链
		{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {2, 1}, {3, 2}},                 // 密集图
	}
	for i, n := range []int{7, 5, 6, 4} {
		eng, _ := New(n)
		for _, ed := range ess[i] {
			if err := eng.AddEdge(ed[0], ed[1]); err != nil {
				return fmt.Errorf("selfcheck %d: %w", i, err)
			}
		}
		eng.Compute()
		doms := naiveDoms(eng.g)
		for b := 0; b < n; b++ {
			id, _ := eng.IDom(b)
			if want := naiveIDom(doms, b); id != want { // 不变量 3
				return fmt.Errorf("selfcheck %d: idom(%d)=%d, naive=%d", i, b, id, want)
			}
			for a := 0; a < n; a++ {
				bit := doms[b]&(1<<a) != 0
				bad1 := eng.Dominates(a, b) != bit                       // 不变量 1
				bad2 := bit && a != b && id >= 0 && doms[id]&(1<<a) == 0 // 不变量 2
				if bad1 || bad2 {
					return fmt.Errorf("selfcheck %d: pair (%d,%d)", i, a, b)
				}
			}
		}
	}
	// 不变量 4：四类拒绝互不相同、状态不变、仍可使用。
	_, err0 := New(0)
	eng, _ := New(3)
	if err := eng.AddEdge(0, 1); err != nil {
		return err
	}
	errs := []error{err0, eng.AddEdge(0, 3), eng.AddEdge(1, 1), eng.AddEdge(0, 1)}
	seen := map[error]bool{}
	for i, ei := range errs {
		if ei == nil || seen[ei] {
			return fmt.Errorf("fault %d: nil or duplicate error", i)
		}
		seen[ei] = true
	}
	if eng.EdgeCount() != 1 {
		return errors.New("rejected AddEdge changed state")
	}
	eng.Compute()
	if !eng.Dominates(0, 1) {
		return errors.New("graph unusable after rejections")
	}
	return nil
}

// naiveDoms 朴素定点迭代求支配集位掩码（自检专用，内置图 n≤64）。
func naiveDoms(g *dgraph.Graph) []uint64 {
	n := g.N()
	reach := g.Reachable()
	preds := g.Preds()
	var full uint64
	doms := make([]uint64, n)
	for v := 0; v < n; v++ {
		if reach[v] {
			full |= 1 << v
			doms[v] = ^uint64(0)
		}
	}
	doms[0] = 1
	for changed := true; changed; {
		changed = false
		for v := 1; v < n; v++ {
			if !reach[v] {
				continue
			}
			s := full | 1<<v
			for _, p := range preds[v] {
				if reach[p] { // 不可达前驱不产生从 0 出发的路径，跳过
					s &= doms[p] | 1<<v
				}
			}
			if s != doms[v] {
				doms[v] = s
				changed = true
			}
		}
	}
	return doms
}

// naiveIDom 从支配集取被其它支配者支配的那个；不可达返回 -1。
func naiveIDom(doms []uint64, v int) int {
	rest := doms[v] &^ (1 << v)
	if rest == 0 {
		return int(doms[v]) - 1 // 只剩自身位（入口）得 0；空集（不可达）得 -1
	}
	for c := 0; c < 64; c++ { // c 是 idom ⟺ rest\{c} 中每个 d 都满足 d ∈ doms[c]
		if rest&(1<<c) != 0 && rest&^(1<<c)&^doms[c] == 0 {
			return c
		}
	}
	return -1
}
