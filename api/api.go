// Package api 是半朴素传递闭包求值器的对外接口。
// 依赖方向：api → semi → rel；不允许反向依赖。
package api

import (
	"errors"
	"sync"

	"ontology/rel"
	"ontology/semi"
)

// 三类输入错误互为不同的哨兵，可用 errors.Is 判定。
var (
	ErrEmptyNode     = errors.New("edge endpoint must not be the empty string")
	ErrDuplicateEdge = errors.New("edge added more than once")
	ErrSelfLoop      = errors.New("self-loop edges are rejected")
)

// Evaluator 持有一份不可变边集与其惰性求值状态，求值结果可被并发读取。
type Evaluator struct {
	edges [][2]string
	eng   *semi.Engine
	once  sync.Once
}

// New 校验全部边并构造求值器。任一非法即整体失败：校验先于任何状态构造，
// 因此被拒不会留下任何痕迹。
func New(edges [][2]string) (*Evaluator, error) {
	seen := rel.New()
	ts := make([]rel.T, 0, len(edges))
	for _, e := range edges {
		x, y := e[0], e[1]
		switch {
		case x == "" || y == "":
			return nil, ErrEmptyNode
		case x == y:
			return nil, ErrSelfLoop
		}
		t := rel.T{X: x, Y: y}
		if seen.Has(t) {
			return nil, ErrDuplicateEdge
		}
		seen.Add(t)
		ts = append(ts, t)
	}
	return &Evaluator{edges: edges, eng: semi.New(ts)}, nil
}

// Eval 返回传递闭包（(x,y) 字典序，去重）；惰性求值且幂等，可并发调用。
func (v *Evaluator) Eval() [][2]string {
	v.once.Do(func() { v.eng.Eval() })
	ts := v.eng.Eval()
	out := make([][2]string, len(ts))
	for i, t := range ts {
		out[i] = [2]string{t.X, t.Y}
	}
	return out
}

// Size 返回闭包元组数。
func (v *Evaluator) Size() int { return len(v.Eval()) }

// SelfCheck 用内置边集逐条核验四条不变量，全部成立返回 nil。
func (v *Evaluator) SelfCheck() error {
	got := rel.New() // 不变量 1：与朴素 BFS 可达性闭包逐元组相同
	for _, t := range v.Eval() {
		got.Add(rel.T{X: t[0], Y: t[1]})
	}
	if !setsEqual(got, referenceClosure(v.edges)) {
		return errors.New("invariant 1 violated: closure differs from BFS reachability")
	}
	rows := v.eng.Rows() // 不变量 2：能执行到此即终止；末轮 Delta 必须为空
	if len(rows) == 0 || len(rows[len(rows)-1].Delta) != 0 {
		return errors.New("invariant 2 violated: last round delta is non-empty")
	}
	entered := rel.New() // 不变量 3：每元组恰在一轮进入 Delta，且 Delta 之并恰为 path
	for _, r := range rows {
		for _, t := range r.Delta {
			if entered.Has(t) {
				return errors.New("invariant 3 violated: tuple entered delta more than once")
			}
			entered.Add(t)
		}
	}
	if !setsEqual(entered, got) {
		return errors.New("invariant 3 violated: union of deltas differs from path")
	}
	before := v.Size() // 不变量 4：三类坏输入命中各自哨兵，且不影响已有状态
	for _, c := range []struct {
		es   [][2]string
		want error
	}{
		{[][2]string{{"", "x"}}, ErrEmptyNode},
		{[][2]string{{"a", "a"}}, ErrSelfLoop},
		{[][2]string{{"a", "b"}, {"a", "b"}}, ErrDuplicateEdge},
	} {
		if _, err := New(c.es); !errors.Is(err, c.want) {
			return errors.New("invariant 4 violated: bad input not rejected with its sentinel")
		}
	}
	if v.Size() != before {
		return errors.New("invariant 4 violated: state changed after rejection")
	}
	return nil
}

// referenceClosure 用最朴素的逐结点 BFS 求可达集（走 ≥1 条边；无环回则无自环）。
func referenceClosure(edges [][2]string) *rel.Set {
	adj := make(map[string][]string)
	nodes := make(map[string]struct{})
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		nodes[e[0]], nodes[e[1]] = struct{}{}, struct{}{}
	}
	clos := rel.New()
	for s := range nodes {
		seen := make(map[string]struct{})
		queue := append([]string{}, adj[s]...)
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			if _, ok := seen[x]; ok {
				continue
			}
			seen[x] = struct{}{}
			queue = append(queue, adj[x]...)
		}
		for t := range seen {
			clos.Add(rel.T{X: s, Y: t})
		}
	}
	return clos
}

func setsEqual(a, b *rel.Set) bool {
	return a.Size() == b.Size() && a.Diff(b).Size() == 0
}
