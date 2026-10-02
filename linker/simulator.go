package linker

// naiveSim 是按规范逐条重写的朴素参考实现：每次 Evaluate 都从头重算
// 可达集合、解析集合与状态转移，供随机测试与正式实现做对照。
// 它不做并发优化（测试在单线程下驱动），数据模型也独立于 module。

type naiveState int

const (
	nvRegistered naiveState = iota
	nvLinked
	nvEvaluating
	nvEvaluated
	nvErrored
)

type naiveMod struct {
	def      ModuleDef
	state    naiveState
	err      string
	init     map[string]bool // 仅 let 导出
	resolved map[string]binding
	bodyRuns int
}

type naiveSim struct {
	mods  map[string]*naiveMod
	order []string
}

func newNaiveSim() *naiveSim {
	return &naiveSim{mods: map[string]*naiveMod{}}
}

func (n *naiveSim) deps(d ModuleDef) []string {
	out := make([]string, 0, len(d.Imports)+len(d.ReExports))
	for _, st := range d.Imports {
		out = append(out, st.Source)
	}
	for _, r := range d.ReExports {
		out = append(out, r.Source)
	}
	return out
}

func (n *naiveSim) localKind(m *naiveMod, name string) (BindingKind, bool) {
	for _, e := range m.def.Exports {
		if e.Name == name {
			return e.Kind, true
		}
	}
	return 0, false
}

// naiveResolve 递归实现 ResolveExport；seen 为本次顶层调用共享的解析集合。
func (n *naiveSim) naiveResolve(mName, name string, seen map[[2]string]bool) (binding, bool, bool) {
	k := [2]string{mName, name}
	if seen[k] {
		return binding{}, false, false
	}
	seen[k] = true
	m := n.mods[mName]

	if _, ok := n.localKind(m, name); ok {
		return binding{module: mName, name: name}, true, false
	}
	for _, r := range m.def.ReExports {
		if !r.IsStar() && r.Name == name {
			return n.naiveResolve(r.Source, r.From, seen)
		}
	}
	var got binding
	have := false
	for _, r := range m.def.ReExports {
		if !r.IsStar() {
			continue
		}
		b, ok, ambig := n.naiveResolve(r.Source, name, seen)
		if ambig {
			return binding{}, false, true
		}
		if !ok {
			continue
		}
		if have && b != got {
			return binding{}, false, true
		}
		got, have = b, true
	}
	if have {
		return got, true, false
	}
	return binding{}, false, false
}

// naiveReach 从根做先序 DFS（跳过已非 registered 的模块），
// 返回先序新链接模块列表；missing 为第一个未登记来源（含根）。
func (n *naiveSim) naiveReach(root string) (list []string, missing string) {
	seen := map[string]bool{}
	var dfs func(x string) bool
	dfs = func(x string) bool {
		if seen[x] {
			return true
		}
		m, ok := n.mods[x]
		if !ok {
			missing = x
			return false
		}
		seen[x] = true
		if m.state != nvRegistered {
			return true
		}
		list = append(list, x)
		for _, d := range n.deps(m.def) {
			if !dfs(d) {
				return false
			}
		}
		return true
	}
	if !dfs(root) {
		return nil, missing
	}
	return list, ""
}

// evalResult 为朴素模拟器的求值返回。
type naiveResult struct {
	appended []string
	ok       bool
	err      string
	rej      error // 拒绝
}

func (n *naiveSim) evaluate(root string) naiveResult {
	if !validName(root) {
		return naiveResult{rej: ErrInvalidArg}
	}
	m, ok := n.mods[root]
	if !ok {
		return naiveResult{rej: ErrNotFound}
	}
	if m.state == nvEvaluated {
		return naiveResult{appended: []string{}, ok: true}
	}
	if m.state == nvErrored {
		return naiveResult{appended: []string{}, ok: false, err: m.err}
	}

	list, missing := n.naiveReach(root)
	if missing != "" {
		return naiveResult{rej: ErrNotFound}
	}

	for _, mn := range list {
		mm := n.mods[mn]
		for _, st := range mm.def.Imports {
			for _, b := range st.Bindings {
				bd, found, ambig := n.naiveResolve(st.Source, b, map[[2]string]bool{})
				if !found {
					return naiveResult{rej: &LinkError{Ambiguous: ambig, Module: mn, Source: st.Source, Name: b}}
				}
				mm.resolved[b] = bd
			}
		}
		for _, r := range mm.def.ReExports {
			if r.IsStar() {
				continue
			}
			if _, found, ambig := n.naiveResolve(r.Source, r.From, map[[2]string]bool{}); !found {
				return naiveResult{rej: &LinkError{Ambiguous: ambig, Module: mn, Source: r.Source, Name: r.From}}
			}
		}
	}

	// 提交链接。
	for _, mn := range list {
		mm := n.mods[mn]
		mm.state = nvLinked
		for _, e := range mm.def.Exports {
			if e.Kind == KindFunction {
				mm.init[e.Name] = true
			}
		}
	}

	start := len(n.order)
	ok2, ev := n.eval(root)
	return naiveResult{appended: append([]string(nil), n.order[start:]...), ok: ok2, err: ev}
}

func (n *naiveSim) eval(mn string) (bool, string) {
	m := n.mods[mn]
	switch m.state {
	case nvEvaluated:
		return true, ""
	case nvErrored:
		return false, m.err
	case nvEvaluating:
		return true, ""
	}
	m.state = nvEvaluating
	for _, d := range n.deps(m.def) {
		if ok, e := n.eval(d); !ok {
			m.state = nvErrored
			m.err = e
			return false, e
		}
	}
	m.bodyRuns++
	if e, threw := n.runBody(m); threw {
		m.state = nvErrored
		m.err = e
		return false, e
	}
	m.state = nvEvaluated
	n.order = append(n.order, mn)
	return true, ""
}

func (n *naiveSim) runBody(m *naiveMod) (string, bool) {
	for _, st := range m.def.Body {
		switch st.Kind {
		case StepInit:
			m.init[st.Name] = true
		case StepRead:
			var b binding
			if st.Module == m.def.Name {
				b = binding{module: m.def.Name, name: st.Name}
			} else {
				b = m.resolved[st.Name]
			}
			d := n.mods[b.module]
			if k, _ := n.localKind(d, b.name); k == KindLet && !d.init[b.name] {
				return "TDZ " + b.module + "." + b.name, true
			}
		case StepThrow:
			return st.Message, true
		}
	}
	return "", false
}

func (n *naiveSim) add(def ModuleDef) error {
	if _, _, _, err := validateDef(def); err != nil {
		return err
	}
	if _, exists := n.mods[def.Name]; exists {
		return ErrNameExists
	}
	if len(n.mods) >= maxModules {
		return ErrTooMany
	}
	m := &naiveMod{
		def:      copyDef(def),
		state:    nvRegistered,
		init:     map[string]bool{},
		resolved: map[string]binding{},
	}
	for _, e := range def.Exports {
		if e.Kind == KindLet {
			m.init[e.Name] = false
		}
	}
	n.mods[def.Name] = m
	return nil
}

func (n *naiveSim) status(name string) (StatusResult, bool) {
	m, ok := n.mods[name]
	if !ok {
		return StatusResult{}, false
	}
	lets := map[string]bool{}
	for _, e := range m.def.Exports {
		if e.Kind == KindLet {
			lets[e.Name] = m.init[e.Name]
		}
	}
	var st ModuleStatus
	switch m.state {
	case nvRegistered:
		st = StatusRegistered
	case nvLinked:
		st = StatusLinked
	case nvEvaluating:
		st = StatusEvaluating
	case nvEvaluated:
		st = StatusEvaluated
	case nvErrored:
		st = StatusErrored
	}
	return StatusResult{Status: st, Lets: lets, Error: m.err}, true
}

func (n *naiveSim) orderSnapshot() []string {
	return append([]string(nil), n.order...)
}
