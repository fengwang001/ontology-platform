package dlabel

import "sort"

// SetRule 声明或替换单个敏感标签的判定规则（管理员操作）。
//
// 校验顺序（固定）：非管理员 → 对象类型不存在 → 规则结构非法 →
// 引用不存在的属性 → 提交后形成标签循环依赖。
// 任一检查失败则本次变更整体不生效（不推进数据版本）。
func (p *Platform) SetRule(subject string, r Rule) error {
	return p.ReplaceRules(subject, r.ObjectType, []Rule{r})
}

// DeleteRule 删除标签判定规则（管理员操作）。
func (p *Platform) DeleteRule(subject, ot, tag string) error {
	e := p.beginAudit("DeleteRule", subject, ot, tag, "")
	var err error
	defer func() { p.finishAudit(e, nil, err) }()
	if !p.isAdmin(subject) {
		err = ErrNotAdmin
		return err
	}
	p.store.commitMu.Lock()
	defer p.store.commitMu.Unlock()
	cat := p.store.currentCatalog()
	if _, ok := cat.types[ot]; !ok {
		err = ErrUnknownObjectType
		return err
	}
	if _, ok := cat.rules[ot][tag]; !ok {
		err = ErrUnknownTag
		return err
	}
	for other, body := range cat.rules[ot] {
		if other == tag {
			continue
		}
		for _, dep := range exprTags(body) {
			if dep == tag {
				err = &Error{Code: CodeInvalidRule, Msg: "cannot delete tag " + tag + ": referenced by tag " + other}
				return err
			}
		}
	}
	p.store.current.Add(1)
	newCat := p.store.advanceCatalog()
	delete(newCat.rules[ot], tag)
	p.store.pruneOld()
	return nil
}

// ReplaceRules 在一次原子变更中替换某对象类型下的一组标签规则，
// 允许规则之间相互引用（循环仍会被拒绝）。变更整体成功或整体不生效。
func (p *Platform) ReplaceRules(subject, ot string, rules []Rule) error {
	e := p.beginAudit("ReplaceRules", subject, ot, "", formatRules(rules))
	err := p.replaceRules(subject, ot, rules)
	p.finishAudit(e, nil, err)
	return err
}

func (p *Platform) replaceRules(subject, ot string, rules []Rule) error {
	if !p.isAdmin(subject) {
		return ErrNotAdmin
	}
	p.store.commitMu.Lock()
	defer p.store.commitMu.Unlock()

	cat := p.store.currentCatalog()
	obj, ok := cat.types[ot]
	if !ok {
		return ErrUnknownObjectType
	}

	// 候选规则集 = 现有规则叠加本次替换。
	candidate := make(map[string]Expr, len(cat.rules[ot])+len(rules))
	for tag, body := range cat.rules[ot] {
		candidate[tag] = body
	}
	knownAttrs := map[string]struct{}{}
	for a := range obj.attrs {
		knownAttrs[a] = struct{}{}
	}

	// 第一步：结构校验与“引用不存在的属性”（特权错误第一优先）。
	// 标签引用暂以候选标签全集作为已知集合，允许同批规则互相引用。
	tagsInBatch := map[string]struct{}{}
	for _, r := range rules {
		if r.ObjectType != "" && r.ObjectType != ot {
			return &Error{Code: CodeInvalidRule, Msg: "rule object type mismatch: " + r.Tag}
		}
		if r.Tag == "" {
			return &Error{Code: CodeInvalidRule, Msg: "rule has empty tag name"}
		}
		tagsInBatch[r.Tag] = struct{}{}
		if err := validateExpr(r.Body); err != nil {
			return err
		}
		for _, attr := range r.referencedAttrs() {
			if _, ok := knownAttrs[attr]; !ok {
				return &Error{Code: CodeMissingAttribute, Msg: "rule of tag " + r.Tag + " references missing attribute: " + attr}
			}
		}
	}
	for _, r := range rules {
		candidate[r.Tag] = r.Body
	}

	// 标签引用必须指向同类型下存在的标签。
	knownTags := map[string]struct{}{}
	for t := range candidate {
		knownTags[t] = struct{}{}
	}
	for _, r := range rules {
		for _, t := range r.referencedTags() {
			if _, ok := knownTags[t]; !ok {
				return &Error{Code: CodeUnknownTag, Msg: "rule of tag " + r.Tag + " references unknown tag: " + t}
			}
		}
	}

	// 第二步：在最终规则图上做循环依赖检测（特权错误第二优先）。
	if err := detectCycle(candidate); err != nil {
		return err
	}

	p.store.current.Add(1)
	newCat := p.store.advanceCatalog()
	for _, r := range rules {
		newCat.rules[ot][r.Tag] = r.Body
	}
	p.store.pruneOld()
	return nil
}

// detectCycle 在规则图上以确定性顺序（标签名排序）检测环。
func detectCycle(rules map[string]Expr) error {
	tags := make([]string, 0, len(rules))
	for t := range rules {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var dfs func(tag string) error
	dfs = func(tag string) error {
		switch state[tag] {
		case done:
			return nil
		case onStack:
			return &Error{Code: CodeCyclicRule, Msg: "cyclic tag dependency at tag: " + tag}
		}
		state[tag] = onStack
		deps := exprTags(rules[tag])
		for _, d := range deps {
			if _, ok := rules[d]; !ok {
				continue
			}
			if err := dfs(d); err != nil {
				return err
			}
		}
		state[tag] = done
		return nil
	}
	for _, t := range tags {
		if err := dfs(t); err != nil {
			return err
		}
	}
	return nil
}

// SetGrant 按标签为主体声明可读/可写/可见范围授权（管理员操作）。
// 授权与具体实例无关，随目录版本生效；旧快照继续看到旧授权。
func (p *Platform) SetGrant(subject string, g Grant) error {
	e := p.beginAudit("SetGrant", subject, "", g.Tag, formatGrant(g))
	var err error
	defer func() { p.finishAudit(e, nil, err) }()
	if !p.isAdmin(subject) {
		err = ErrNotAdmin
		return err
	}
	if g.Subject == "" {
		g.Subject = subject
	}
	p.store.commitMu.Lock()
	defer p.store.commitMu.Unlock()
	p.store.current.Add(1)
	cat := p.store.advanceCatalog()
	cat.grants[grantKey{subject: g.Subject, tag: g.Tag}] = g
	p.store.pruneOld()
	return nil
}
