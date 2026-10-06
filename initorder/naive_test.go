package initorder

import "sort"

// naiveSession 是规格说明的朴素直译实现，用于与优化实现对照：
// 登记逻辑逐条校验，求解时每个单元独立地做函数引用闭包展开，
// 每一轮都扫描全部剩余单元选取最靠前的可初始化者。
type naiveSession struct {
	predeclared map[string]bool
	vars        map[string]int // 非空白变量名 -> 单元下标
	funcs       map[string]int // 函数名 -> 函数下标
	units       []varUnit
	funcDecls   []funcDecl
	decls       []declRef
}

func newNaiveSession(predeclared []string) *naiveSession {
	n := &naiveSession{
		predeclared: map[string]bool{},
		vars:        map[string]int{},
		funcs:       map[string]int{},
	}
	for _, p := range predeclared {
		n.predeclared[p] = true
	}
	return n
}

func (n *naiveSession) declared(name string) bool {
	if n.predeclared[name] {
		return true
	}
	if _, ok := n.vars[name]; ok {
		return true
	}
	_, ok := n.funcs[name]
	return ok
}

func (n *naiveSession) registerVars(vars []string, refs []string) error {
	// 参数非法优先。
	if len(vars) == 0 {
		return &InvalidArgumentError{Reason: "empty variable list"}
	}
	for _, v := range vars {
		if !isValidIdent(v) {
			return &InvalidArgumentError{Reason: "invalid variable identifier: " + v}
		}
	}
	for _, r := range refs {
		if !isValidIdent(r) {
			return &InvalidArgumentError{Reason: "invalid identifier in refs: " + r}
		}
		if isBlankIdent(r) {
			return &InvalidArgumentError{Reason: "blank identifier in refs"}
		}
	}
	// 重复声明其次。
	seen := map[string]bool{}
	for _, v := range vars {
		if isBlankIdent(v) {
			continue
		}
		if seen[v] || n.declared(v) {
			return &DuplicateDeclarationError{Name: v}
		}
		seen[v] = true
	}
	idx := len(n.units)
	n.units = append(n.units, varUnit{vars: append([]string(nil), vars...), refs: append([]string(nil), refs...)})
	for _, v := range vars {
		if !isBlankIdent(v) {
			n.vars[v] = idx
		}
	}
	n.decls = append(n.decls, declRef{kind: DeclKindVars, index: idx})
	return nil
}

func (n *naiveSession) registerFunc(name string, refs []string) error {
	if !isValidIdent(name) {
		return &InvalidArgumentError{Reason: "invalid func name: " + name}
	}
	if isBlankIdent(name) {
		return &InvalidArgumentError{Reason: "func name is blank identifier"}
	}
	for _, r := range refs {
		if !isValidIdent(r) {
			return &InvalidArgumentError{Reason: "invalid identifier in refs: " + r}
		}
		if isBlankIdent(r) {
			return &InvalidArgumentError{Reason: "blank identifier in refs"}
		}
	}
	if n.declared(name) {
		return &DuplicateDeclarationError{Name: name}
	}
	idx := len(n.funcDecls)
	n.funcDecls = append(n.funcDecls, funcDecl{name: name, refs: append([]string(nil), refs...)})
	n.funcs[name] = idx
	n.decls = append(n.decls, declRef{kind: DeclKindFunc, index: idx})
	return nil
}

// requiredVars 计算单元初始化前必须完成的变量集合（含自身变量，
// 自身引用同样构成环）；沿函数引用朴素地逐单元展开，不做记忆化。
func (n *naiveSession) requiredVars(u varUnit) map[string]bool {
	req := map[string]bool{}
	visited := map[string]bool{}
	queue := append([]string(nil), u.refs...)
	for len(queue) > 0 {
		r := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if n.predeclared[r] {
			continue
		}
		if _, ok := n.vars[r]; ok {
			req[r] = true
			continue
		}
		fi, ok := n.funcs[r]
		if !ok || visited[r] {
			continue
		}
		visited[r] = true
		queue = append(queue, n.funcDecls[fi].refs...)
	}
	return req
}

func (n *naiveSession) solve() (*Solution, error) {
	// 未声明引用：按登记次序扫描全部声明，优先于初始化环。
	for di, d := range n.decls {
		var refs []string
		var name string
		if d.kind == DeclKindVars {
			refs = n.units[d.index].refs
		} else {
			refs = n.funcDecls[d.index].refs
			name = n.funcDecls[d.index].name
		}
		best := ""
		for _, r := range refs {
			if !n.declared(r) && (best == "" || r < best) {
				best = r
			}
		}
		if best != "" {
			return nil, &UndeclaredReferenceError{DeclIndex: di, Kind: d.kind, Name: name, Ident: best}
		}
	}

	// 每个单元独立展开传递依赖。
	reqs := make([]map[string]bool, len(n.units))
	for i, u := range n.units {
		reqs[i] = n.requiredVars(u)
	}

	// 逐轮扫描全部剩余单元，选取源码最靠前且依赖均已完成的单元。
	done := map[string]bool{}
	remaining := make([]int, len(n.units))
	for i := range remaining {
		remaining[i] = i
	}
	var order []int
	for len(remaining) > 0 {
		picked := -1
		for pos, ui := range remaining {
			ready := true
			for r := range reqs[ui] {
				if !done[r] {
					ready = false
					break
				}
			}
			if ready {
				picked = pos
				_ = ui
				break
			}
		}
		if picked < 0 {
			var vars []string
			for _, ui := range remaining {
				for _, v := range n.units[ui].vars {
					if !isBlankIdent(v) {
						vars = append(vars, v)
					}
				}
			}
			return nil, &InitializationCycleError{Vars: vars}
		}
		ui := remaining[picked]
		remaining = append(remaining[:picked], remaining[picked+1:]...)
		order = append(order, ui)
		for _, v := range n.units[ui].vars {
			if !isBlankIdent(v) {
				done[v] = true
			}
		}
	}

	sol := &Solution{Order: make([]UnitResult, 0, len(order))}
	for _, ui := range order {
		u := n.units[ui]
		own := map[string]bool{}
		for _, v := range u.vars {
			if !isBlankIdent(v) {
				own[v] = true
			}
		}
		var deps []string
		for r := range reqs[ui] {
			if !own[r] {
				deps = append(deps, r)
			}
		}
		sort.Strings(deps)
		sol.Order = append(sol.Order, UnitResult{
			Unit: ui,
			Vars: append([]string(nil), u.vars...),
			Deps: deps,
		})
	}
	return sol, nil
}
