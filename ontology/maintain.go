package ontology

import "sort"

// applyLinkChange 增量维护全部视图；任何视图失败则所有视图回滚到调用前状态。
// 调用方已在图上完成边变更（AddLink 已加边 / RemoveLink 已删边）。
func (g *Graph) applyLinkChange(link, src, dst string, oldMul, newMul int64, opts LinkOptions) (map[string]struct{}, error) {
	type cand struct {
		vs       *viewState
		newsnap  *snapshot
		affected map[string]struct{}
		reason   string
	}
	cands := make([]cand, 0, len(g.views))
	for _, vs := range g.views {
		hops := g.hopMatches(vs, link, src, dst)
		if len(hops) == 0 {
			continue
		}
		// 运行时环拒绝：优先级低于类型/实例错误（已在调用前完成检查）。
		if opts.RejectCycle && !vs.cycleExcludable && newMul > oldMul && g.detectCycle(vs, hops, src, dst) {
			return nil, errf(KindCycleRejected,
				"view %q: adding %s %s->%s makes one instance occur at two path positions",
				vs.spec.Name, link, src, dst)
		}
		ns := g.buildSnapshot(vs)
		affected := diffAffected(vs.snap.reach, ns.reach)
		if vs.failNext {
			vs.failNext = false
			return nil, errf(KindMaintenanceRollback,
				"view %q: injected maintenance failure at %s %s->%s", vs.spec.Name, link, src, dst)
		}
		hs := append([]int(nil), hops...)
		sort.Ints(hs)
		cands = append(cands, cand{
			vs:       vs,
			newsnap:  ns,
			affected: affected,
			reason:   sprintf("hops=%v recompute layered DP, diff reachable-end sets", hs),
		})
	}
	// 全部判定成功后原子提交；任一失败则上面已直接返回（未改任何 vs.snap）。
	affected := map[string]struct{}{}
	for _, c := range cands {
		c.vs.snap = c.newsnap
		for s := range c.affected {
			affected[s] = struct{}{}
		}
	}
	if g.logf != nil {
		for _, c := range cands {
			g.logf(sprintf("  view=%s basis=%s", c.vs.spec.Name, c.reason))
		}
	}
	return affected, nil
}

// hopMatches 返回该边与视图哪些跳（位置）匹配：链接名相同且两端类型位于对应类型集合。
func (g *Graph) hopMatches(vs *viewState, link, src, dst string) []int {
	var hops []int
	srcT := g.objects[src].typ
	dstT := g.objects[dst].typ
	for k, lname := range vs.spec.Path.Links {
		if lname != link {
			continue
		}
		if contains(vs.spec.Path.Types[k], srcT) && contains(vs.spec.Path.Types[k+1], dstT) {
			hops = append(hops, k)
		}
	}
	return hops
}

// detectCycle 精确判断“刚加入的 src->dst 边”是否参与某条完整声明路径上的环：
// 即存在前缀使某实例在路径上占据两个不同位置。判定方式——
//  1. 对每个匹配跳 k，求“不含新边”时能从任意起点在恰好 k 跳到达 src 的实例集合
//     及其所在起点（preSrc[start]=true 表示 start 可经 k 跳到 src）；
//  2. 跨过新边到 dst（第 k+1 层），再沿后续跳展开，若在任意层 d>=k+1 再次
//     遇到 src，或遇到某个“前缀路径上已经出现过”的实例，则新边成环。
//
// 实现上采用充分条件的可靠检测：从 dst 出发沿跳序做有界集合展开，
// 若能再次到达 src（与新边同类型位置兼容），则存在 src->...->dst（新边）->...->src
// 的闭路径被某条完整声明路径使用。前缀可达性由“src 处于第 k 层可达集合”保证。
func (g *Graph) detectCycle(vs *viewState, hops []int, src, dst string) bool {
	allow := g.allowAt(vs)
	n := len(vs.spec.Path.Links)
	for _, k := range hops {
		// src 是否可能出现在某条完整路径的第 k 层：从任意第 0 层对象做 k 跳正向展开。
		if !g.reachableAtLayer(vs, allow, src, k) {
			continue
		}
		// 从 dst（第 k+1 层）沿后续跳展开，若在 d>k+1 层再次到达 src，则成环。
		cur := map[string]struct{}{dst: {}}
		for d := k + 1; d < n; d++ {
			lname := vs.spec.Path.Links[d]
			next := map[string]struct{}{}
			for u := range cur {
				for v := range g.out[lname][u] {
					if _, okA := allow[d+1][g.objects[v].typ]; !okA {
						continue
					}
					next[v] = struct{}{}
				}
			}
			cur = next
			if _, back := cur[src]; back {
				return true
			}
		}
	}
	return false
}

// reachableAtLayer 判断 target 能否从某个第 0 层允许实例出发，在恰好 layer 跳后到达。
func (g *Graph) reachableAtLayer(vs *viewState, allow []map[string]struct{}, target string, layer int) bool {
	cur := map[string]struct{}{}
	for id, o := range g.objects {
		if _, okA := allow[0][o.typ]; okA {
			cur[id] = struct{}{}
		}
	}
	for d := 0; d < layer; d++ {
		lname := vs.spec.Path.Links[d]
		next := map[string]struct{}{}
		for u := range cur {
			for v := range g.out[lname][u] {
				if _, okA := allow[d+1][g.objects[v].typ]; !okA {
					continue
				}
				next[v] = struct{}{}
			}
		}
		cur = next
	}
	_, ok := cur[target]
	return ok
}

// diffAffected 对比新旧 reach，返回可达终点集合发生变化的起点实例集合（不多不少）。
func diffAffected(old, neu map[reachKey]int64) map[string]struct{} {
	aff := map[string]struct{}{}
	for k, oc := range old {
		if (oc > 0) != (neu[k] > 0) {
			aff[k.start] = struct{}{}
		}
	}
	for k, nc := range neu {
		if nc > 0 {
			if _, existed := old[k]; !existed {
				aff[k.start] = struct{}{}
			}
		}
	}
	return aff
}
