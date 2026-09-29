package ontology

import (
	"fmt"
	"sort"
)

// graph 是经过完整校验、与运行时状态无关的不可变图结构。
// 所有邻接表均按名称排序，保证任何遍历结果可复现。
type graph struct {
	baseNames  []string            // 有序基底名
	viewNames  []string            // 有序视图名
	deps       map[string][]string // 视图 -> 有序且去重的直接依赖
	dependents map[string][]string // 反向边：被改动名 -> 有序且去重的直接下游视图
	topo       []string            // 全部视图的拓扑序：依赖恒排在视图之前
}

// buildGraph 对 Definition 执行全部静态校验。
// 任何一项失败都返回对应的可区分哨兵错误，且没有副作用。
func buildGraph(def Definition, maxViews int) (*graph, error) {
	if maxViews < 0 {
		return nil, fmt.Errorf("%w: maxViews=%d", ErrInvalidLimit, maxViews)
	}

	// 先校验输入中的名称本身（空名属于最基础的输入错误）。
	seen := make(map[string]bool, len(def.Bases)+len(def.Views))
	for _, name := range def.Bases {
		if name == "" {
			return nil, fmt.Errorf("%w: in bases", ErrEmptyName)
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, name)
		}
		seen[name] = true
	}

	viewNames := make([]string, 0, len(def.Views))
	for name := range def.Views {
		viewNames = append(viewNames, name)
	}
	sort.Strings(viewNames)

	for _, name := range viewNames {
		if name == "" {
			return nil, fmt.Errorf("%w: in views", ErrEmptyName)
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, name)
		}
		seen[name] = true
	}

	if maxViews > 0 && len(viewNames) > maxViews {
		return nil, fmt.Errorf("%w: %d > limit %d", ErrTooManyViews, len(viewNames), maxViews)
	}

	deps := make(map[string][]string, len(viewNames))
	dependents := make(map[string][]string)
	for _, v := range viewNames {
		unique := uniqueSorted(def.Views[v])
		for _, dep := range unique {
			if dep == "" {
				return nil, fmt.Errorf("%w: in deps of %q", ErrEmptyName, v)
			}
			if !seen[dep] {
				return nil, fmt.Errorf("%w: %q referenced by view %q", ErrUnknownDep, dep, v)
			}
			dependents[dep] = append(dependents[dep], v)
		}
		deps[v] = unique
	}
	for dep := range dependents {
		sort.Strings(dependents[dep])
	}

	topo, err := topoSort(viewNames, deps)
	if err != nil {
		return nil, err
	}

	baseNames := append([]string(nil), def.Bases...)
	sort.Strings(baseNames)

	return &graph{
		baseNames:  baseNames,
		viewNames:  viewNames,
		deps:       deps,
		dependents: dependents,
		topo:       topo,
	}, nil
}

// uniqueSorted 返回去重后按字典序排列的名称集合。
func uniqueSorted(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// topoSort 用 Kahn 算法求确定性拓扑序。视图数为 0 时返回空切片。
func topoSort(viewNames []string, deps map[string][]string) ([]string, error) {
	indegree := make(map[string]int, len(viewNames))
	inSet := make(map[string]bool, len(viewNames))
	for _, v := range viewNames {
		inSet[v] = true
	}
	viewDependents := make(map[string][]string)
	for _, v := range viewNames {
		for _, dep := range deps[v] {
			if inSet[dep] { // 只有指向视图的边参与视图间拓扑约束
				indegree[v]++
				viewDependents[dep] = append(viewDependents[dep], v)
			}
		}
	}

	ready := make([]string, 0)
	for _, v := range viewNames { // viewNames 已排序，初始化即确定性
		if indegree[v] == 0 {
			ready = append(ready, v)
		}
	}

	order := make([]string, 0, len(viewNames))
	for len(ready) > 0 {
		v := ready[0]
		ready = ready[1:]
		order = append(order, v)
		for _, w := range viewDependents[v] {
			indegree[w]--
			if indegree[w] == 0 {
				ready = insertSorted(ready, w)
			}
		}
	}

	if len(order) != len(viewNames) {
		var unresolved []string
		for _, v := range viewNames {
			if indegree[v] > 0 {
				unresolved = append(unresolved, v)
			}
		}
		sort.Strings(unresolved)
		return nil, fmt.Errorf("%w: views %v", ErrCycle, unresolved)
	}
	return order, nil
}

func contains(list []string, target string) bool {
	for _, x := range list {
		if x == target {
			return true
		}
	}
	return false
}

// insertSorted 维持就绪堆的字典序，使同层视图顺序确定。
func insertSorted(list []string, s string) []string {
	i := sort.SearchStrings(list, s)
	list = append(list, "")
	copy(list[i+1:], list[i:])
	list[i] = s
	return list
}
