package initorder

import (
	"container/heap"
	"sort"
)

// solveLocked 在持有读锁的前提下对会话当前快照求解，不修改会话状态。
//
// 算法概要：
//  1. 按登记次序扫描全部声明，发现未声明引用立即报错（优先于初始化环）。
//  2. 在函数引用子图上做 Tarjan 强连通分解，把相互递归的函数折叠成分量；
//     按分量逆拓扑序（汇点先出）一次性算出每个分量可达的变量集合 D(C)，
//     被大量单元引用的函数只展开一次。
//  3. 用“待满足前驱计数 + 最小堆”模拟贪心选取：计数归零的单元进入按
//     源码次序排序的堆，每轮弹出堆顶即规则要求的“最靠前且可选”单元，
//     无需逐轮扫描全部剩余单元。
func (s *Session) solveLocked() (*Solution, *solveStats, error) {
	stats := &solveStats{}

	// 第一步：未声明引用检查，覆盖全部已登记声明（含无人使用的函数）。
	isDeclared := func(name string) bool {
		if s.predeclared[name] {
			return true
		}
		if _, ok := s.varOwner[name]; ok {
			return true
		}
		if _, ok := s.funcIndex[name]; ok {
			return true
		}
		return false
	}
	for di, d := range s.decls {
		var refs []string
		var name string
		if d.kind == DeclKindVars {
			refs = s.units[d.index].refs
		} else {
			refs = s.funcs[d.index].refs
			name = s.funcs[d.index].name
		}
		best := ""
		for _, r := range refs {
			if !isDeclared(r) && (best == "" || r < best) {
				best = r
			}
		}
		if best != "" {
			return nil, stats, &UndeclaredReferenceError{
				DeclIndex: di,
				Kind:      d.kind,
				Name:      name,
				Ident:     best,
			}
		}
	}

	// 第二步：构造函数引用图并做强连通分解。
	numFuncs := len(s.funcs)
	funcSucc := make([][]int, numFuncs)    // 函数 -> 函数 边（去重）
	funcVars := make([][]string, numFuncs) // 函数体直接引用的变量名（去重）
	for fi, f := range s.funcs {
		seenSucc := map[int]bool{}
		for _, r := range f.refs {
			if s.predeclared[r] {
				continue
			}
			if _, ok := s.varOwner[r]; ok {
				funcVars[fi] = append(funcVars[fi], r)
				continue
			}
			gi := s.funcIndex[r]
			if !seenSucc[gi] {
				seenSucc[gi] = true
				funcSucc[fi] = append(funcSucc[fi], gi)
			}
		}
	}
	compOf, comps := tarjanSCC(funcSucc)
	numComps := len(comps)
	stats.FuncExpansions = numFuncs // 每个函数体只被展开一次

	// 按分量逆拓扑序（Tarjan 汇点先出）计算每个分量可达的变量集合 D(C)。
	compVars := make([][]string, numComps)
	for ci, comp := range comps {
		set := map[string]bool{}
		for _, fi := range comp {
			for _, v := range funcVars[fi] {
				set[v] = true
			}
			for _, w := range funcSucc[fi] {
				cj := compOf[w]
				if cj == ci {
					continue
				}
				for _, v := range compVars[cj] {
					set[v] = true
				}
			}
		}
		vs := make([]string, 0, len(set))
		for v := range set {
			vs = append(vs, v)
		}
		sort.Strings(vs)
		compVars[ci] = vs
	}

	// 第三步：拆分每个单元的直接变量引用与直接函数分量引用。
	numUnits := len(s.units)
	unitVarRefs := make([][]string, numUnits)
	unitCompRefs := make([][]int, numUnits)
	for ui, u := range s.units {
		seenComp := map[int]bool{}
		for _, r := range u.refs {
			if s.predeclared[r] {
				continue
			}
			if _, ok := s.varOwner[r]; ok {
				unitVarRefs[ui] = append(unitVarRefs[ui], r)
				continue
			}
			ci := compOf[s.funcIndex[r]]
			if !seenComp[ci] {
				seenComp[ci] = true
				unitCompRefs[ui] = append(unitCompRefs[ui], ci)
			}
		}
	}

	// 第四步：前驱计数 + 最小堆模拟贪心选取。
	varToUnits := map[string][]int{} // 变量 -> 直接引用它的单元
	for ui, vars := range unitVarRefs {
		for _, v := range vars {
			varToUnits[v] = append(varToUnits[v], ui)
		}
	}
	varToComps := map[string][]int{} // 变量 -> 可达它的函数分量
	for ci, vars := range compVars {
		for _, v := range vars {
			varToComps[v] = append(varToComps[v], ci)
		}
	}
	compToUnits := map[int][]int{} // 分量 -> 直接引用它的单元
	for ui, cs := range unitCompRefs {
		for _, ci := range cs {
			compToUnits[ci] = append(compToUnits[ci], ui)
		}
	}

	pendingUnit := make([]int, numUnits)
	for ui := range s.units {
		pendingUnit[ui] = len(unitVarRefs[ui]) + len(unitCompRefs[ui])
	}
	pendingComp := make([]int, numComps)
	for ci := range comps {
		pendingComp[ci] = len(compVars[ci])
	}

	ready := &unitHeap{}
	unitDone := make([]bool, numUnits)
	compDone := make([]bool, numComps)

	var satisfyComp func(ci int)
	decUnit := func(ui int) {
		if unitDone[ui] {
			return
		}
		pendingUnit[ui]--
		if pendingUnit[ui] == 0 {
			heap.Push(ready, ui)
		}
	}
	satisfyComp = func(ci int) {
		if compDone[ci] {
			return
		}
		compDone[ci] = true
		for _, ui := range compToUnits[ci] {
			decUnit(ui)
		}
	}
	decComp := func(ci int) {
		if compDone[ci] {
			return
		}
		pendingComp[ci]--
		if pendingComp[ci] == 0 {
			satisfyComp(ci)
		}
	}

	for ci := range comps {
		if pendingComp[ci] == 0 {
			satisfyComp(ci)
		}
	}
	for ui := range s.units {
		if pendingUnit[ui] == 0 {
			heap.Push(ready, ui)
		}
	}

	var order []int
	for ready.Len() > 0 {
		ui := heap.Pop(ready).(int)
		if unitDone[ui] {
			continue
		}
		unitDone[ui] = true
		order = append(order, ui)
		for _, v := range s.units[ui].vars {
			if isBlankIdent(v) {
				continue
			}
			for _, ci := range varToComps[v] {
				decComp(ci)
			}
			for _, uj := range varToUnits[v] {
				decUnit(uj)
			}
		}
	}
	stats.UnitsCompleted = len(order)

	// 剩余单元无一可选取：初始化环，按源码次序报告全部无法完成的变量。
	if len(order) < numUnits {
		var vars []string
		for ui := 0; ui < numUnits; ui++ {
			if unitDone[ui] {
				continue
			}
			for _, v := range s.units[ui].vars {
				if !isBlankIdent(v) {
					vars = append(vars, v)
				}
			}
		}
		return nil, stats, &InitializationCycleError{Vars: vars}
	}

	// 汇总结果：次序 + 每个单元的传递依赖变量集合（判定依据）。
	sol := &Solution{Order: make([]UnitResult, 0, numUnits)}
	for _, ui := range order {
		u := s.units[ui]
		own := map[string]bool{}
		for _, v := range u.vars {
			if !isBlankIdent(v) {
				own[v] = true
			}
		}
		set := map[string]bool{}
		for _, v := range unitVarRefs[ui] {
			if !own[v] {
				set[v] = true
			}
		}
		for _, ci := range unitCompRefs[ui] {
			for _, v := range compVars[ci] {
				if !own[v] {
					set[v] = true
				}
			}
		}
		deps := make([]string, 0, len(set))
		for v := range set {
			deps = append(deps, v)
		}
		sort.Strings(deps)
		sol.Order = append(sol.Order, UnitResult{
			Unit: ui,
			Vars: append([]string(nil), u.vars...),
			Deps: deps,
		})
	}
	return sol, stats, nil
}

// tarjanSCC 对函数引用图做强连通分解，返回每个节点的分量编号与按
// 逆拓扑序（汇点先出）排列的分量列表。
func tarjanSCC(succ [][]int) ([]int, [][]int) {
	n := len(succ)
	compOf := make([]int, n)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
		compOf[i] = -1
	}
	var stack []int
	var comps [][]int
	counter := 0
	var strongconnect func(v int)
	strongconnect = func(v int) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ[v] {
			if index[w] < 0 {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				compOf[w] = len(comps)
				if w == v {
					break
				}
			}
			comps = append(comps, comp)
		}
	}
	for v := 0; v < n; v++ {
		if index[v] < 0 {
			strongconnect(v)
		}
	}
	return compOf, comps
}

// unitHeap 是按单元登记次序排序的最小堆。
type unitHeap []int

func (h unitHeap) Len() int           { return len(h) }
func (h unitHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h unitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *unitHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *unitHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
