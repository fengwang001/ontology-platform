package module

// 朴素模拟：严格按规则逐步写成，与真实实现结构不同——
// 模块用切片线性查找，可达集合每次 Evaluate 从头重算（显式栈先序遍历），
// 导出解析每次顶层调用新建解析集合，Read 在运行时重新解析而不读缓存。
// 用于与真实实现做随机对照。

type naiveModule struct {
	name      string
	imports   []Import
	exports   []Export
	reexports []Reexport
	body      []Step
	deps      []string
	state     State
	errValue  string
	inited    map[string]bool
}

type naiveSession struct {
	mods  []*naiveModule
	order []string
}

func newNaiveSession() *naiveSession { return &naiveSession{} }

func (ns *naiveSession) find(name string) *naiveModule {
	for _, m := range ns.mods {
		if m.name == name {
			return m
		}
	}
	return nil
}

func (ns *naiveSession) add(name string, imports []Import, exports []Export, reexports []Reexport, body []Step) *Reject {
	if rej := validateSpec(name, imports, exports, reexports, body); rej != nil {
		return rej
	}
	if ns.find(name) != nil {
		return &Reject{Reason: RejectNameRegistered, Name: name}
	}
	if len(ns.mods) >= maxModules {
		return &Reject{Reason: RejectTooManyModules, Name: name}
	}
	m := &naiveModule{
		name:      name,
		imports:   imports,
		exports:   exports,
		reexports: reexports,
		body:      body,
		state:     StateRegistered,
		inited:    make(map[string]bool),
	}
	for _, im := range imports {
		m.deps = append(m.deps, im.Source)
	}
	for _, re := range reexports {
		m.deps = append(m.deps, re.Source)
	}
	ns.mods = append(ns.mods, m)
	return nil
}

type naiveKey struct{ m, n string }

// naiveResolve 朴素导出解析：每次顶层调用传入新建的解析集合。
func (ns *naiveSession) naiveResolve(m, n string, seen map[naiveKey]bool) ([2]string, resolveResult) {
	k := naiveKey{m, n}
	if seen[k] {
		return [2]string{}, resolveNotFound
	}
	seen[k] = true
	mod := ns.find(m)
	if mod == nil {
		return [2]string{}, resolveNotFound
	}
	for _, ex := range mod.exports {
		if ex.Name == n {
			return [2]string{m, n}, resolveFound
		}
	}
	for _, re := range mod.reexports {
		if !re.Star && re.Name == n {
			return ns.naiveResolve(re.Source, re.SourceName, seen)
		}
	}
	var star [2]string
	hasStar := false
	for _, re := range mod.reexports {
		if !re.Star {
			continue
		}
		bind, r := ns.naiveResolve(re.Source, n, seen)
		if r == resolveAmbiguous {
			return [2]string{}, resolveAmbiguous
		}
		if r == resolveFound {
			if hasStar && star != bind {
				return [2]string{}, resolveAmbiguous
			}
			star = bind
			hasStar = true
		}
	}
	if hasStar {
		return star, resolveFound
	}
	return [2]string{}, resolveNotFound
}

func naiveFail(m *naiveModule, source, name string, r resolveResult) *Reject {
	if r == resolveAmbiguous {
		return &Reject{Reason: RejectLinkAmbiguous, Module: m.name, Source: source, Name: name}
	}
	return &Reject{Reason: RejectLinkError, Module: m.name, Source: source, Name: name}
}

func (ns *naiveSession) evaluate(root string) (EvalResult, *Reject) {
	if !validName(root) {
		return EvalResult{}, invalidf("根模块名 %q 为空或超过 %d 字节", root, maxNameBytes)
	}
	rootMod := ns.find(root)
	if rootMod == nil {
		return EvalResult{}, &Reject{Reason: RejectModuleNotFound, Name: root}
	}
	if rootMod.state == StateEvaluated {
		return EvalResult{OK: true}, nil
	}
	if rootMod.state == StateError {
		return EvalResult{OK: false, ErrValue: rootMod.errValue}, nil
	}

	// 链接：显式栈先序遍历，从头重算可达集合，只进入已登记模块。
	var preorder []*naiveModule
	visited := map[string]bool{root: true}
	if rootMod.state == StateRegistered {
		preorder = append(preorder, rootMod)
	}
	type frame struct {
		m    *naiveModule
		next int
	}
	stack := []frame{{rootMod, 0}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.m.state != StateRegistered || f.next >= len(f.m.deps) {
			stack = stack[:len(stack)-1]
			continue
		}
		dep := f.m.deps[f.next]
		f.next++
		d := ns.find(dep)
		if d == nil {
			return EvalResult{}, &Reject{Reason: RejectModuleNotFound, Name: dep}
		}
		if visited[dep] {
			continue
		}
		visited[dep] = true
		if d.state == StateRegistered {
			preorder = append(preorder, d)
		}
		stack = append(stack, frame{d, 0})
	}

	// 按先序做导出解析：先导入后具名转出；全部通过前不提交任何修改。
	for _, m := range preorder {
		for _, im := range m.imports {
			for _, n := range im.Names {
				if _, r := ns.naiveResolve(im.Source, n, make(map[naiveKey]bool)); r != resolveFound {
					return EvalResult{}, naiveFail(m, im.Source, n, r)
				}
			}
		}
		for _, re := range m.reexports {
			if re.Star {
				continue
			}
			if _, r := ns.naiveResolve(re.Source, re.SourceName, make(map[naiveKey]bool)); r != resolveFound {
				return EvalResult{}, naiveFail(m, re.Source, re.SourceName, r)
			}
		}
	}
	for _, m := range preorder {
		m.state = StateLinked
		for _, ex := range m.exports {
			if ex.Kind == Func {
				m.inited[ex.Name] = true
			}
		}
	}

	// 求值：深度优先后序，Read 现场重新解析。
	before := len(ns.order)
	errValue, ok := ns.eval(rootMod)
	return EvalResult{
		Appended: append([]string(nil), ns.order[before:]...),
		OK:       ok,
		ErrValue: errValue,
	}, nil
}

func (ns *naiveSession) eval(m *naiveModule) (string, bool) {
	switch m.state {
	case StateEvaluated:
		return "", true
	case StateError:
		return m.errValue, false
	case StateEvaluating:
		return "", true
	}
	m.state = StateEvaluating
	for _, dep := range m.deps {
		if errValue, ok := ns.eval(ns.find(dep)); !ok {
			m.state = StateError
			m.errValue = errValue
			return errValue, false
		}
	}
	for _, st := range m.body {
		switch st.Kind {
		case StepInit:
			m.inited[st.Name] = true
		case StepRead:
			d, localName := m, st.Name
			if st.Module != m.name {
				bind, _ := ns.naiveResolve(st.Module, st.Name, make(map[naiveKey]bool))
				d, localName = ns.find(bind[0]), bind[1]
			}
			isLet := false
			for _, ex := range d.exports {
				if ex.Name == localName && ex.Kind == Let {
					isLet = true
				}
			}
			if isLet && !d.inited[localName] {
				errValue := "TDZ " + d.name + "." + localName
				m.state = StateError
				m.errValue = errValue
				return errValue, false
			}
		case StepThrow:
			m.state = StateError
			m.errValue = st.Message
			return st.Message, false
		}
	}
	m.state = StateEvaluated
	ns.order = append(ns.order, m.name)
	return "", true
}

func (ns *naiveSession) status(name string) (ModuleStatus, *Reject) {
	m := ns.find(name)
	if m == nil {
		return ModuleStatus{}, &Reject{Reason: RejectModuleNotFound, Name: name}
	}
	st := ModuleStatus{State: m.state, ErrValue: m.errValue, LetInit: make(map[string]bool)}
	for _, ex := range m.exports {
		if ex.Kind == Let {
			st.LetInit[ex.Name] = m.inited[ex.Name]
		}
	}
	return st, nil
}

func (ns *naiveSession) getOrder() []string {
	return append([]string(nil), ns.order...)
}
