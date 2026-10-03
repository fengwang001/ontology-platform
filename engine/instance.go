package engine

import (
	"ontology/model"
)

// New 基于已通过 Validate 与选择校验的图构造实例：
// 裁剪有效图、计算传递闭包、并令 Start 节点产出 1 个令牌（立即沿图收敛）。
func New(g *model.Graph, ch model.Choice) *Instance {
	n := g.N
	eff := g.EffectiveOut(ch)
	in := &Instance{
		n:        n,
		kind:     append([]model.NodeType(nil), g.Kinds...),
		act:      make([]int, n+1),
		arr:      make([][]int, n+1),
		ein:      make([][]int, n+1),
		fires:    make([]int, n+1),
		eout:     eff,
		canReach: model.CanReach(n, eff),
	}
	structIn := g.In()
	for v := 1; v <= n; v++ {
		if g.Kinds[v] == model.AndJoin || g.Kinds[v] == model.OrJoin {
			in.arr[v] = make([]int, len(structIn[v]))
			in.ein[v] = append([]int(nil), structIn[v]...)
		}
	}
	start := 1
	for v := 1; v <= n; v++ {
		if g.Kinds[v] == model.Start {
			start = v
			break
		}
	}
	in.deliverEdge(0, start, 1)
	in.settle()
	return in
}

// deliver 把 count 个令牌送达节点 v，按节点类型即时分发。
func (in *Instance) deliver(v, count int) {
	switch in.kind[v] {
	case model.Start:
		// Start 恰 1 出边，令牌继续向下。
		in.deliverEdge(v, in.eout[v][0], count)
	case model.Task:
		in.act[v] += count
	case model.AndSplit:
		for _, w := range in.eout[v] {
			in.deliverEdge(v, w, count)
		}
	case model.XorSplit, model.OrSplit:
		for _, w := range in.eout[v] {
			in.deliverEdge(v, w, count)
		}
	case model.End:
		in.endCount += count
	}
	// AndJoin/OrJoin 的入边计数由 deliverEdge 处理。
}

// deliverEdge 沿结构边 (u,v) 送出 count 个令牌；若 v 是汇合则落到对应入边槽。
func (in *Instance) deliverEdge(u, v, count int) {
	if in.kind[v] == model.AndJoin || in.kind[v] == model.OrJoin {
		idx := 0
		for i, src := range in.ein[v] {
			if src == u {
				idx = i
				break
			}
		}
		in.arr[v][idx] += count
		return
	}
	in.deliver(v, count)
}
