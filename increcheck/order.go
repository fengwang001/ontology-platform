package increcheck

// order.go：确定性次序工具。
// 所有集合遍历都收敛到字典序；SCC 组按组内最小标识排序；
// 组间按签名依赖做「被依赖者优先」的稳定拓扑序，
// 使任何编辑序列下「谁被重检、以什么次序重检」客观唯一。

import "sort"

// sortedStrings 返回字典序副本，绝不修改入参。
func sortedStrings(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

// contains 报告 v 是否在 xs 中。
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// sortComps 使每个分量内部有序，并按分量最小成员排序。
func sortComps(comps [][]string) {
	for _, c := range comps {
		sort.Strings(c)
	}
	sort.Slice(comps, func(i, j int) bool {
		return comps[i][0] < comps[j][0]
	})
}

// topoGroups 把签名 SCC 组排成「被依赖者先于依赖者」的确定次序。
// 只统计跨越不同组的边；同处就绪的组按最小成员排序，次序因此唯一。
func topoGroups(g *DepGraph, groups [][]string) [][]string {
	groupOf := map[string]int{}
	for gi, members := range groups {
		for _, m := range members {
			groupOf[m] = gi
		}
	}
	indeg := make([]int, len(groups))
	succ := make([]map[int]struct{}, len(groups))
	for gi := range groups {
		succ[gi] = map[int]struct{}{}
	}
	// 边 dep -> owner：dep 所在组必须先于 owner 所在组。
	for owner, gi := range groupOf {
		for _, dep := range g.sigDepsOf(owner) {
			gj, ok := groupOf[dep]
			if !ok || gj == gi {
				continue
			}
			if _, dup := succ[gj][gi]; !dup {
				succ[gj][gi] = struct{}{}
				indeg[gi]++
			}
		}
	}

	ready := []int{}
	for gi, d := range indeg {
		if d == 0 {
			ready = append(ready, gi)
		}
	}
	sortGroupsReady(groups, ready)

	result := make([][]string, 0, len(groups))
	for len(ready) > 0 {
		gi := ready[0]
		ready = ready[1:]
		result = append(result, groups[gi])
		fresh := []int{}
		for sj := range succ[gi] {
			indeg[sj]--
			if indeg[sj] == 0 {
				fresh = append(fresh, sj)
			}
		}
		ready = append(ready, fresh...)
		sortGroupsReady(groups, ready)
	}
	return result
}

func sortGroupsReady(groups [][]string, ready []int) {
	sort.Slice(ready, func(i, j int) bool {
		return groups[ready[i]][0] < groups[ready[j]][0]
	})
}
