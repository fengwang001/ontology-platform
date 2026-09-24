// Package walk 在给定访问预算下做确定性、可续传的广度优先遍历。
// 预算计量单位是「本次访问的节点数」；出边按字典序惰性展开，
// 队列驻留量受预算约束（见 DESIGN.md 第 4 节）。
package walk

import (
	"errors"

	"ontology/graph"
)

// ErrNodeMissing 表示续点或边引用了图中不存在的节点（含续传间图被删改）。
var ErrNodeMissing = errors.New("walk: checkpoint references missing node")

// Walk 从 cp 续点出发，在 g 上至多访问 budget 个节点。
// 遍历不修改图；同一 (g, cp, budget) 可被多协程并发调用。
func Walk(g *graph.Graph, cp Checkpoint, budget int) (Result, error) {
	if err := validate(g, cp); err != nil {
		return Result{Next: cp}, err
	}
	if budget < 0 {
		budget = 0
	}
	res := Result{Next: cloneCP(cp)}
	if cp.Done || budget == 0 {
		return res, nil
	}

	discovered := make(map[string]struct{}, len(cp.Visited)+budget)
	for _, id := range cp.Visited {
		discovered[id] = struct{}{}
	}
	curNodes := make(map[string]struct{}, len(cp.Cur))
	for _, f := range cp.Cur {
		curNodes[f.Node] = struct{}{}
	}

	peak := len(cp.Cur) + len(cp.Pending) + len(cp.Next)

	for len(res.Visited) < budget {
		if len(res.Next.Pending) == 0 {
			if !produce(g, &res.Next, discovered, &res.edgesExamined) {
				// 本层展开器耗尽：换层或结束。
				if len(res.Next.Next) == 0 {
					res.Next.Done = true
					break
				}
				res.Next.Cur = res.Next.Next
				curNodes = make(map[string]struct{}, len(res.Next.Cur))
				for _, f := range res.Next.Cur {
					curNodes[f.Node] = struct{}{}
				}
				res.Next.Next = nil
			}
		}
		id := res.Next.Pending[0]
		res.Next.Pending = res.Next.Pending[1:]
		res.Visited = append(res.Visited, id)
		res.Next.Visited = append(res.Next.Visited, id)
		discovered[id] = struct{}{}
		if _, preplaced := curNodes[id]; !preplaced {
			res.Next.Next = append(res.Next.Next, Frame{Node: id})
		}
		if n := len(res.Next.Cur) + len(res.Next.Pending) + len(res.Next.Next); n > peak {
			peak = n
		}
	}

	res.visitedCount = len(res.Visited)
	res.peakQueue = peak
	return res, nil
}

// produce 推进 cur 头部展开器的出边游标，直到为 pending 补入一个
// 新发现的下层节点。返回 false 表示所有展开器均已耗尽。
func produce(g *graph.Graph, cp *Checkpoint, seen map[string]struct{}, examined *int) bool {
	for len(cp.Cur) > 0 {
		head := &cp.Cur[0]
		out, ok := g.Out(head.Node)
		if !ok {
			return false
		}
		for head.Cursor < len(out) {
			target := out[head.Cursor]
			head.Cursor++
			*examined++
			if _, dup := seen[target]; dup || !g.Has(target) {
				continue
			}
			seen[target] = struct{}{}
			cp.Pending = append(cp.Pending, target)
			if head.Cursor == len(out) {
				cp.Cur = cp.Cur[1:]
			}
			return true
		}
		cp.Cur = cp.Cur[1:]
	}
	return false
}

func validate(g *graph.Graph, cp Checkpoint) error {
	check := func(id string) error {
		if !g.Has(id) {
			return missingError(id)
		}
		return nil
	}
	for _, f := range cp.Cur {
		if err := check(f.Node); err != nil {
			return err
		}
	}
	for _, f := range cp.Next {
		if err := check(f.Node); err != nil {
			return err
		}
	}
	for _, ids := range [][]string{cp.Pending, cp.Visited} {
		for _, id := range ids {
			if err := check(id); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneCP(cp Checkpoint) Checkpoint {
	cp.Cur = append([]Frame(nil), cp.Cur...)
	cp.Pending = append([]string(nil), cp.Pending...)
	cp.Next = append([]Frame(nil), cp.Next...)
	cp.Visited = append([]string(nil), cp.Visited...)
	return cp
}
