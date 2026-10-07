package compensation

import "sort"

// NaiveStep 是朴素串行参考模型对单个子操作的描述。
//
// 它刻意与生产实现（StepSpec/Operation/Inverse 接口）不共享任何
// 调度或状态机代码，只共享对象图原语，从而构成一个真正独立的
// "神谕实现"：单线程、无并发、按固定合法拓扑顺序执行。
type NaiveStep struct {
	Name  string
	Keys  []string
	Delta int64 // 正向 Add 增量；逆操作即 Add(-Delta)
	// ApplyFail 为 true 时该子操作生效失败，且不修改对象图。
	ApplyFail bool
	// UndoFail 为 true 时该子操作对应的逆操作失败；
	// 失败的逆操作按 best-effort 语义不产生改动。
	UndoFail bool
}

// NaiveBranch 是参考模型中的分支声明。
type NaiveBranch struct {
	Name      string
	DependsOn []string
	Steps     []NaiveStep
}

// NaiveUndoFailure 记录一次逆操作失败（分支 + 子操作下标）。
type NaiveUndoFailure struct {
	Branch    string
	StepIndex int
}

// NaiveResult 是朴素模型一次运行的全部可观察结果。
type NaiveResult struct {
	// OpFail 记录自身失败的分支及其失败子操作下标。
	OpFail map[string]int
	// PassiveFail 记录因上游失败而被动失败的分支 -> 触发它的上游。
	PassiveFail map[string]string
	// UndoFailures 按参考模型的实际尝试顺序记录全部逆操作失败。
	UndoFailures []NaiveUndoFailure
}

// NaiveRun 在给定对象图上串行执行一份朴素声明。
//
// 正向顺序：取依赖图的一个合法拓扑序（按名字排序打破平局，保证
// 可复现）；逐条分支顺序执行其全部子操作，上游失败时本分支整体
// 被动失败、一个子操作都不启动。
// 补偿顺序：正向拓扑序整体反转（因此被依赖者一定在所有依赖者
// 之后补偿），分支内按已生效子操作逆序撤销。
func NaiveRun(g *Graph, branches []NaiveBranch) NaiveResult {
	res := NaiveResult{
		OpFail:      make(map[string]int),
		PassiveFail: make(map[string]string),
	}
	byName := make(map[string]*NaiveBranch)
	names := make([]string, 0, len(branches))
	for i := range branches {
		byName[branches[i].Name] = &branches[i]
		names = append(names, branches[i].Name)
	}
	sort.Strings(names)

	topo := naiveTopoOrder(names, byName)

	type applied struct {
		branch string
		step   int
	}
	var appliedStack []applied
	succeeded := make(map[string]bool)
	failed := make(map[string]bool)

	for _, bname := range topo {
		b := byName[bname]
		upstreamFailed := ""
		for _, dep := range b.DependsOn {
			if failed[dep] {
				upstreamFailed = dep
				break
			}
		}
		if upstreamFailed != "" {
			failed[bname] = true
			res.PassiveFail[bname] = upstreamFailed
			continue
		}
		stepFailed := -1
		for i, st := range b.Steps {
			if st.ApplyFail {
				stepFailed = i
				break
			}
			g.Add(st.Keys[0], st.Delta)
			appliedStack = append(appliedStack, applied{bname, i})
		}
		if stepFailed >= 0 {
			failed[bname] = true
			res.OpFail[bname] = stepFailed
		} else {
			succeeded[bname] = true
		}
	}

	if len(failed) == 0 {
		return res
	}

	// 全局逆序撤销：appliedStack 的逆序天然同时满足
	// "分支内逆序"与"被依赖分支后补偿"（因为正向拓扑序保证
	// 被依赖者的全部子操作都先于下游分支入栈）。
	compensated := make(map[applied]bool)
	for i := len(appliedStack) - 1; i >= 0; i-- {
		ap := appliedStack[i]
		if compensated[ap] {
			continue
		}
		compensated[ap] = true
		st := byName[ap.branch].Steps[ap.step]
		if st.UndoFail {
			res.UndoFailures = append(res.UndoFailures,
				NaiveUndoFailure{Branch: ap.branch, StepIndex: ap.step})
			continue
		}
		g.Add(st.Keys[0], -st.Delta)
	}
	return res
}

// naiveTopoOrder 返回名字排序破平局的依赖拓扑序（Kahn 算法）。
func naiveTopoOrder(names []string, byName map[string]*NaiveBranch) []string {
	indeg := make(map[string]int)
	dependents := make(map[string][]string)
	for _, n := range names {
		indeg[n] = len(byName[n].DependsOn)
		for _, dep := range byName[n].DependsOn {
			dependents[dep] = append(dependents[dep], n)
		}
	}
	var ready []string
	for _, n := range names {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	var order []string
	for len(ready) > 0 {
		sort.Strings(ready)
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		deps := append([]string(nil), dependents[n]...)
		sort.Strings(deps)
		for _, d := range deps {
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	return order
}
