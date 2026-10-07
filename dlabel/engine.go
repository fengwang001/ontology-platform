package dlabel

import "sort"

// grantMode 区分授权裁决的维度。
type grantMode int

const (
	grantRead grantMode = iota
	grantWrite
	grantVisibility
)

// engine 在某个不可变目录快照与某个实例视图上求值全部标签。
//
// 关键性质：
//   - 标签状态永远按需重算，引擎不提供任何跨调用缓存；
//   - 每个参与判定的属性按闭包集合精确读取一次（readAttr 记账），
//     因此读取数量只与实际参与判定的属性数量有关，与已登记标签总数无关；
//   - 标签闭包按名称排序后求值，配合记忆化，求值顺序完全确定。
type engine struct {
	p          *Platform
	cat        *catalogState
	objectType string
	instance   string
	lookup     func(attr string) Value

	rules map[string]Expr // 本类型标签规则

	visited  map[string]bool // 当前 DFS 栈
	finished map[string]bool
	carried  map[string]bool

	attrLoaded map[string]struct{} // 已经实际读取过的属性
	attrOrder  []string

	instrument bool // 是否累计平台级观测计数器
}

func newEngine(p *Platform, cat *catalogState, ot, id string, lookup func(string) Value, instrument bool) *engine {
	rules := map[string]Expr{}
	if m, ok := cat.rules[ot]; ok {
		for tag, body := range m {
			rules[tag] = body
		}
	}
	return &engine{
		p:          p,
		cat:        cat,
		objectType: ot,
		instance:   id,
		lookup:     lookup,
		rules:      rules,
		visited:    map[string]bool{},
		finished:   map[string]bool{},
		carried:    map[string]bool{},
		attrLoaded: map[string]struct{}{},
		instrument: instrument,
	}
}

// readAttr 是规则求值唯一的属性入口：同一属性在一次判定中只读取一次，
// 读取动作被记账（平台计数器与本次判定的 attrsRead）。
func (e *engine) readAttr(attr string) Value {
	if _, ok := e.attrLoaded[attr]; !ok {
		e.attrLoaded[attr] = struct{}{}
		e.attrOrder = append(e.attrOrder, attr)
		if e.instrument {
			e.p.attrRead.Add(1)
		}
	}
	return e.lookup(attr)
}

// AttrsRead 返回本次判定实际读取的属性（按首次读取顺序、去重）。
func (e *engine) AttrsRead() []string {
	out := make([]string, len(e.attrOrder))
	copy(out, e.attrOrder)
	return out
}

// evalTag 求值单个标签，带 DFS 循环检测与记忆化。
func (e *engine) evalTag(tag string) error {
	if e.finished[tag] {
		return nil
	}
	if e.visited[tag] {
		return &Error{Code: CodeCyclicRule, Msg: "cyclic tag dependency at tag: " + tag}
	}
	body, ok := e.rules[tag]
	if !ok {
		// 目录内标签引用均已在规则提交时校验；防御性处理。
		return &Error{Code: CodeUnknownTag, Msg: "unknown tag: " + tag}
	}
	e.visited[tag] = true
	if e.instrument {
		e.p.evalCount.Add(1)
	}
	v, err := evalExpr(body, evalEnv{
		readAttr: e.readAttr,
		tagValue: func(t string) (bool, error) {
			if err := e.evalTag(t); err != nil {
				return false, err
			}
			return e.carried[t], nil
		},
	})
	if err != nil {
		return err
	}
	delete(e.visited, tag)
	e.finished[tag] = true
	e.carried[tag] = v
	return nil
}

// evaluateAll 按确定顺序求值本实例携带状态可能影响裁决的全部标签闭包，
// 返回携带标签集合与每个标签的裁决依据。
func (e *engine) evaluateAll() (map[string]struct{}, []TagVerdict, error) {
	tags := make([]string, 0, len(e.rules))
	for t := range e.rules {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		if err := e.evalTag(t); err != nil {
			return nil, nil, err
		}
	}
	carried := map[string]struct{}{}
	basis := make([]TagVerdict, 0, len(tags))
	for _, t := range tags {
		c := e.carried[t]
		basis = append(basis, TagVerdict{Tag: t, Carried: c})
		if c {
			carried[t] = struct{}{}
		}
	}
	return carried, basis, nil
}

// closureOf 返回从目标标签出发、按规则依赖静态可达的全部标签（含目标），
// 结果排序去重。只求值该闭包：与本次权限裁决无关的标签及其引用的属性
// 完全不会被读取，因此判定读取的属性数不随已登记标签总数增长。
func (e *engine) closureOf(targets []string) []string {
	seen := map[string]struct{}{}
	stack := append([]string(nil), targets...)
	for len(stack) > 0 {
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[t]; ok {
			continue
		}
		body, ok := e.rules[t]
		if !ok {
			continue
		}
		seen[t] = struct{}{}
		for _, dep := range exprTags(body) {
			if _, ok := seen[dep]; !ok {
				stack = append(stack, dep)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// evaluateTargets 只求值目标标签闭包，返回携带集合与依据。
func (e *engine) evaluateTargets(targets []string) (map[string]struct{}, []TagVerdict, error) {
	closure := e.closureOf(targets)
	for _, t := range closure {
		if err := e.evalTag(t); err != nil {
			return nil, nil, err
		}
	}
	carried := map[string]struct{}{}
	basis := make([]TagVerdict, 0, len(closure))
	for _, t := range closure {
		c := e.carried[t]
		basis = append(basis, TagVerdict{Tag: t, Carried: c})
		if c {
			carried[t] = struct{}{}
		}
	}
	return carried, basis, nil
}

// relevantTags 静态计算某主体在给定模式与属性集合下可能影响裁决的标签：
// 即该主体拥有授权行、且授权属性覆盖目标属性集合（可见性授权忽略属性范围）
// 的全部标签。
func relevantTags(cat *catalogState, subject string, attrs []string, includeVisibility bool) []string {
	want := map[string]struct{}{}
	for _, a := range attrs {
		want[a] = struct{}{}
	}
	seen := map[string]struct{}{}
	for k, g := range cat.grants {
		if k.subject != subject {
			continue
		}
		if includeVisibility {
			seen[k.tag] = struct{}{}
		}
		for _, a := range g.Attrs {
			if a == "*" {
				seen[k.tag] = struct{}{}
				break
			}
			if _, ok := want[a]; ok {
				seen[k.tag] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// evalFor 只求值给定目标标签闭包（按需判定单标签时使用）。
func (e *engine) evalFor(target string) error {
	return e.evalTag(target)
}

// decideAttr 在携带标签集合上合并某主体对某属性的最终结论。
//
// 只收集集合 {是否有结论, 是否存在拒绝, 是否存在允许} 三个布尔量，
// 标签按名称排序遍历，保证过程与结论都不依赖规则求值顺序。
// 通配符 "*" 授权表示该类型全部属性。
func decideAttr(cat *catalogState, subject string, carried map[string]struct{}, attr string, mode grantMode) Decision {
	tags := make([]string, 0, len(carried))
	for t := range carried {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	sawAny, sawDeny, sawAllow := false, false, false
	for _, tag := range tags {
		g, ok := cat.grants[grantKey{subject: subject, tag: tag}]
		if !ok {
			continue
		}
		matched := false
		for _, a := range g.Attrs {
			if a == "*" || a == attr {
				matched = true
				break
			}
		}
		if mode == grantRead || mode == grantWrite {
			if !matched {
				continue
			}
		}
		eff := g.Read
		switch mode {
		case grantWrite:
			eff = g.Write
		case grantVisibility:
			eff = g.Visibility
		}
		sawAny = true
		if eff == EffectDeny {
			sawDeny = true
		} else {
			sawAllow = true
		}
	}
	return mergeEffects(sawAny, sawDeny, sawAllow)
}
