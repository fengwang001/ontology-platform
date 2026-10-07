package ontology

import "sort"

// evaluator 在一次请求的固定版本状态上求值标签规则。
//
// 属性取值按名缓存：一次判定中每个实际参与的属性只从版本状态中读取一次，
// 读取次数（loads）对外可观测，且仅与实际参与本次判定的属性数量相关，
// 与该对象类型已登记的规则总数无关。缓存随请求结束即丢弃，绝不跨请求复用，
// 因此不存在过期标签状态。
type evaluator struct {
	st       *state
	inst     InstanceRef
	values   map[string]Value
	loaded   map[string]bool
	memo     map[string]tagResult
	visiting map[string]bool
	loads    int
}

type tagResult struct {
	carried bool
	err     *Error
}

func newEvaluator(st *state, inst InstanceRef) *evaluator {
	return &evaluator{
		st:       st,
		inst:     inst,
		values:   map[string]Value{},
		loaded:   map[string]bool{},
		memo:     map[string]tagResult{},
		visiting: map[string]bool{},
	}
}

func (ev *evaluator) loadAttr(name string) Value {
	if ev.loaded[name] {
		return ev.values[name]
	}
	ev.loads++
	v := ev.st.values[ev.inst.key()][name]
	ev.values[name] = v
	ev.loaded[name] = true
	return v
}

// evalTag 按需计算实例在当前版本状态下是否携带给定标签。
// 判定基于属性的真实取值，与发起判定的主体是否可读这些属性无关；
// 求值过程不向外返回任何属性取值，只返回布尔结论。
func (ev *evaluator) evalTag(tag string) (bool, *Error) {
	if r, ok := ev.memo[tag]; ok {
		return r.carried, r.err
	}
	if ev.visiting[tag] {
		return false, errf(ErrCyclicRule, "cyclic dependency through tag %q", tag)
	}
	ev.visiting[tag] = true
	defer delete(ev.visiting, tag)
	rule, ok := ev.st.config.rules[tag]
	if !ok {
		err := errf(ErrUnknownTag, "tag %q is not registered", tag)
		ev.memo[tag] = tagResult{err: err}
		return false, err
	}
	v, err := ev.evalExpr(rule.Expr)
	if err != nil {
		ev.memo[tag] = tagResult{err: err}
		return false, err
	}
	b, ok := asBool(v)
	if !ok {
		err := errf(ErrRuleType, "rule %q did not evaluate to a boolean", tag)
		ev.memo[tag] = tagResult{err: err}
		return false, err
	}
	ev.memo[tag] = tagResult{carried: b}
	return b, nil
}

func (ev *evaluator) evalExpr(x Expr) (Value, *Error) {
	switch e := x.(type) {
	case ConstExpr:
		return e.V, nil
	case AttrExpr:
		return ev.loadAttr(e.Name), nil
	case TagExpr:
		return ev.evalTag(e.Tag)
	case NotExpr:
		v, err := ev.evalExpr(e.X)
		if err != nil {
			return nil, err
		}
		b, ok := asBool(v)
		if !ok {
			return nil, errf(ErrRuleType, "not() expects a boolean operand")
		}
		return !b, nil
	case AndExpr:
		for _, sub := range e.Xs {
			v, err := ev.evalExpr(sub)
			if err != nil {
				return nil, err
			}
			b, ok := asBool(v)
			if !ok {
				return nil, errf(ErrRuleType, "and() expects boolean operands")
			}
			if !b {
				return false, nil
			}
		}
		return true, nil
	case OrExpr:
		for _, sub := range e.Xs {
			v, err := ev.evalExpr(sub)
			if err != nil {
				return nil, err
			}
			b, ok := asBool(v)
			if !ok {
				return nil, errf(ErrRuleType, "or() expects boolean operands")
			}
			if b {
				return true, nil
			}
		}
		return false, nil
	case CmpExpr:
		l, err := ev.evalExpr(e.L)
		if err != nil {
			return nil, err
		}
		r, err := ev.evalExpr(e.R)
		if err != nil {
			return nil, err
		}
		res, ok := compareValues(e.Op, l, r)
		if !ok {
			return nil, errf(ErrRuleType, "values are not comparable with %s", e.Op)
		}
		return res, nil
	}
	return nil, errf(ErrRuleType, "unknown expression node %T", x)
}

// staticValidate 按固定优先级执行判定前的静态校验：
// 未知属性引用（含请求了未声明的属性）> 未知标签引用 > 循环依赖。
// requested 为 nil 表示校验该对象类型上的全部规则（用于 TagsOf）。
// 对象类型未注册时无法做规则校验，返回 nil，由调用方随后报告 ErrUnknownObjectType。
func staticValidate(cfg *config, inst InstanceRef, requested []string) *Error {
	ot, ok := cfg.objectTypes[inst.Type]
	if !ok {
		return nil
	}
	req := sortedCopy(requested)
	for _, a := range req {
		if !ot.hasAttribute(a) {
			return errf(ErrUnknownAttribute, "attribute %q is not declared on object type %q", a, ot.Name)
		}
	}
	var roots []string
	if requested == nil {
		for tag, r := range cfg.rules {
			if r.ObjectType == inst.Type {
				roots = append(roots, tag)
			}
		}
	} else {
		want := make(map[string]struct{}, len(req))
		for _, a := range req {
			want[a] = struct{}{}
		}
		for tag, r := range cfg.rules {
			if r.ObjectType != inst.Type {
				continue
			}
			for a := range r.Expr.Attrs() {
				if _, ok := want[a]; ok {
					roots = append(roots, tag)
					break
				}
			}
		}
	}
	sort.Strings(roots)
	closure := map[string]TagRule{}
	queue := roots
	for len(queue) > 0 {
		tag := queue[0]
		queue = queue[1:]
		if _, done := closure[tag]; done {
			continue
		}
		r, ok := cfg.rules[tag]
		if !ok {
			continue // 未知标签由引用方在校验未知标签引用时报告
		}
		closure[tag] = r
		queue = append(queue, sortedKeys(r.Expr.Tags())...)
	}
	for _, tag := range sortedKeys(ruleSet(closure)) {
		for _, a := range sortedKeys(closure[tag].Expr.Attrs()) {
			if !ot.hasAttribute(a) {
				return errf(ErrUnknownAttribute, "rule %q references undeclared attribute %q on object type %q", tag, a, ot.Name)
			}
		}
	}
	for _, tag := range sortedKeys(ruleSet(closure)) {
		for _, dep := range sortedKeys(closure[tag].Expr.Tags()) {
			dr, ok := cfg.rules[dep]
			if !ok || dr.ObjectType != inst.Type {
				return errf(ErrUnknownTag, "rule %q references unknown tag %q", tag, dep)
			}
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(tag string) *Error
	visit = func(tag string) *Error {
		color[tag] = gray
		for _, dep := range sortedKeys(closure[tag].Expr.Tags()) {
			if _, inClosure := closure[dep]; !inClosure {
				continue
			}
			switch color[dep] {
			case gray:
				return errf(ErrCyclicRule, "cyclic dependency between rules %q and %q", tag, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[tag] = black
		return nil
	}
	for _, tag := range sortedKeys(ruleSet(closure)) {
		if color[tag] == white {
			if err := visit(tag); err != nil {
				return err
			}
		}
	}
	return nil
}

func ruleSet(m map[string]TagRule) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// governingTags 返回直接管辖给定属性的标签（规则直接引用该属性且属于同一对象类型），
// 按标签名排序，保证判定与日志的顺序确定性。
func governingTags(cfg *config, typeName, attr string) []string {
	var out []string
	for tag, r := range cfg.rules {
		if r.ObjectType != typeName {
			continue
		}
		if _, ok := r.Expr.Attrs()[attr]; ok {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

// checkAccess 对一次属性访问做完整的校验、标签判定与权限合并。
//
// 合并规则（拒绝优先，与求值顺序无关）：对本次访问涉及的全部管辖标签，
// 实例在判定版本上携带且未明确授权的标签只要存在一个，访问即被拒绝；
// 标签未携带、或授权表明确允许时不构成拒绝。实例不携带任何管辖标签时默认允许；
// 携带标签但授权表中没有该主体的记录时默认拒绝。
func checkAccess(st *state, inst InstanceRef, p Principal, attrs []string, forRead bool) (map[string]Value, []TagBasis, int, *Error) {
	cfg := st.config
	if err := staticValidate(cfg, inst, attrs); err != nil {
		return nil, nil, 0, err
	}
	if _, ok := cfg.objectTypes[inst.Type]; !ok {
		return nil, nil, 0, errf(ErrUnknownObjectType, "object type %q is not registered", inst.Type)
	}
	ev := newEvaluator(st, inst)
	tagSet := map[string]struct{}{}
	for _, a := range attrs {
		for _, tag := range governingTags(cfg, inst.Type, a) {
			tagSet[tag] = struct{}{}
		}
	}
	var basis []TagBasis
	for _, tag := range sortedKeys(tagSet) {
		carried, err := ev.evalTag(tag)
		if err != nil {
			return nil, basis, ev.loads, err
		}
		g, hasGrant := cfg.grantFor(tag, p)
		permitted := hasGrant && grantPermits(g, forRead)
		basis = append(basis, TagBasis{
			Tag:       tag,
			Carried:   carried,
			Consulted: carried,
			Allowed:   !carried || permitted,
		})
		if carried && !permitted {
			return nil, basis, ev.loads, errf(ErrPermissionDenied, "access denied")
		}
	}
	if !forRead {
		return nil, basis, ev.loads, nil
	}
	values := make(map[string]Value, len(attrs))
	for _, a := range attrs {
		values[a] = ev.loadAttr(a)
	}
	return values, basis, ev.loads, nil
}

func grantPermits(g Grant, forRead bool) bool {
	if forRead {
		return g.Read
	}
	return g.Write
}

// authorize 校验一次写入的权限，返回判定依据与属性加载次数。
func authorize(st *state, inst InstanceRef, p Principal, attrs []string, forRead bool) ([]TagBasis, int, *Error) {
	_, basis, loads, err := checkAccess(st, inst, p, attrs, forRead)
	return basis, loads, err
}

// readValues 校验一次读取的权限并取出属性取值。
func readValues(st *state, inst InstanceRef, p Principal, attrs []string) (map[string]Value, []TagBasis, int, *Error) {
	return checkAccess(st, inst, p, attrs, true)
}

// visibleTags 计算实例当前携带且对主体可见的标签列表。
// 未在授权表中登记的主体默认不可见任何标签。
func visibleTags(st *state, inst InstanceRef, p Principal) ([]string, []TagBasis, int, *Error) {
	cfg := st.config
	if err := staticValidate(cfg, inst, nil); err != nil {
		return nil, nil, 0, err
	}
	if _, ok := cfg.objectTypes[inst.Type]; !ok {
		return nil, nil, 0, errf(ErrUnknownObjectType, "object type %q is not registered", inst.Type)
	}
	ev := newEvaluator(st, inst)
	var tags []string
	var basis []TagBasis
	for _, tag := range sortedKeys(allRulesOf(cfg, inst.Type)) {
		carried, err := ev.evalTag(tag)
		if err != nil {
			return nil, basis, ev.loads, err
		}
		g, hasGrant := cfg.grantFor(tag, p)
		visible := carried && hasGrant && g.Visible
		basis = append(basis, TagBasis{Tag: tag, Carried: carried, Consulted: carried, Allowed: visible})
		if visible {
			tags = append(tags, tag)
		}
	}
	return tags, basis, ev.loads, nil
}

func allRulesOf(cfg *config, typeName string) map[string]struct{} {
	out := map[string]struct{}{}
	for tag, r := range cfg.rules {
		if r.ObjectType == typeName {
			out[tag] = struct{}{}
		}
	}
	return out
}
