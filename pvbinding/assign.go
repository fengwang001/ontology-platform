package pvbinding

import "sort"

// jointBind 为延迟声明集合求解最优互不相同卷指派。
//
// 入参声明按名称升序排列；候选集已施加节点约束。采用带回溯的穷举搜索：
// 按声明顺序逐个尝试候选卷，通过 (总浪费下界 + 字典序) 剪枝。因为候选
// 卷按 (浪费, 名称) 排序，搜索到的第一个完整解即满足：
//  1. 总浪费最小；
//  2. 总浪费相同时，按声明名称升序所得卷名称序列字典序最小。
//
// 返回 nil 表示不存在可行指派（全有或全无，调用方据此拒绝）。
func jointBind(claims []*Claim, candidates [][]*Volume) []*Volume {
	n := len(claims)
	if n == 0 {
		return nil
	}
	// 过滤候选并按 (浪费升序, 名称升序) 排序。
	ordered := make([][]*Volume, n)
	for i := range claims {
		list := candidates[i]
		sort.SliceStable(list, func(a, b int) bool {
			wa := list[a].Spec.Capacity - claims[i].Spec.RequestCapacity
			wb := list[b].Spec.Capacity - claims[i].Spec.RequestCapacity
			return wa < wb || (wa == wb && list[a].Name < list[b].Name)
		})
		ordered[i] = list
		if len(list) == 0 {
			return nil
		}
	}

	// 每个声明尚未选定时可达到的最小浪费（用于总浪费下界剪枝）。
	minWaste := make([]int64, n+1)
	for i := n - 1; i >= 0; i-- {
		minWaste[i] = minWaste[i+1] + ordered[i][0].Spec.Capacity - claims[i].Spec.RequestCapacity
	}

	chosen := make([]*Volume, n)
	used := make(map[string]bool, n)

	var best []*Volume
	var bestWaste int64

	var dfs func(i int, waste int64)
	dfs = func(i int, waste int64) {
		if i == n {
			if best != nil && waste > bestWaste {
				return
			}
			if best != nil && waste == bestWaste && !lexNamesSmaller(chosen, best) {
				return
			}
			sol := make([]*Volume, n)
			copy(sol, chosen)
			best = sol
			bestWaste = waste
			return
		}
		for _, v := range ordered[i] {
			if used[v.Name] {
				continue
			}
			w := v.Spec.Capacity - claims[i].Spec.RequestCapacity
			// 仅按总浪费严格剪枝：相等下界必须保留以支持字典序比较。
			// 候选按浪费升序，一旦严格越界，本层后续卷也无需再试。
			if best != nil && waste+w+minWaste[i+1] > bestWaste {
				return
			}
			chosen[i] = v
			used[v.Name] = true
			dfs(i+1, waste+w)
			delete(used, v.Name)
		}
	}

	dfs(0, 0)
	return best
}

// lexNamesSmaller 判断 a 的卷名称序列是否比 b 字典序更小（长度相同）。
func lexNamesSmaller(a, b []*Volume) bool {
	for i := range a {
		if a[i].Name != b[i].Name {
			return a[i].Name < b[i].Name
		}
	}
	return false
}
