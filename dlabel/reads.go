package dlabel

import "sort"

// AttrResult 是单个属性读取的对外结果。
// 不携带任何标签判定依据，避免通过结果信道泄露不可读属性的取值信息。
type AttrResult struct {
	Allowed bool
	Present bool
	Value   Value
}

// ReadResult 是一次实例读取的对外结果（仅包含被允许的属性）。
type ReadResult struct {
	Version int64
	Attrs   map[string]AttrResult
}

// snapshotView 在快照版本上构造实例属性的只读视图，并完成错误优先级检查。
//
// 固定检查顺序：
//  1. 快照不可用（已释放或超出保留窗口）；
//  2. 对象类型 / 实例不存在；
//  3. 规则静态错误：缺失属性 → 循环依赖（引擎求值时暴露）。
func (s *Snapshot) prepare(subject, ot, id string) (*engine, *catalogState, *instanceData, error) {
	if err := s.checkUsable(); err != nil {
		return nil, nil, nil, err
	}
	p := s.platform
	cat := p.store.catalogAt(s.version)
	if _, ok := cat.types[ot]; !ok {
		return nil, nil, nil, ErrUnknownObjectType
	}
	inst := p.store.getInstance(ot, id)
	if inst == nil || !inst.existsAt(s.version) {
		return nil, nil, nil, ErrUnknownInstance
	}
	v := s.version
	eng := newEngine(p, cat, ot, id,
		func(attr string) Value {
			val, ok := inst.getAt(attr, v)
			if !ok {
				return NullValue()
			}
			return val
		},
		true)
	return eng, cat, inst, nil
}

// evaluate 在快照上完成标签判定，按错误优先级包装。
func (s *Snapshot) evaluate(eng *engine) (map[string]struct{}, []TagVerdict, error) {
	carried, basis, err := eng.evaluateAll()
	if err != nil {
		return nil, nil, mapRuleError(err)
	}
	return carried, basis, nil
}

// mapRuleError 将引擎错误映射到三类特权错误的固定优先级。
// 缺失属性与循环依赖均可能在求值时暴露；缺失属性优先。
func mapRuleError(err error) error {
	if err == nil {
		return nil
	}
	if code := errorCode(err); code == CodeMissingAttribute {
		return err
	}
	if code := errorCode(err); code == CodeCyclicRule {
		return err
	}
	return err
}

// ReadAttributes 在快照内读取实例的若干属性。
//
// 权限语义：
//   - 管理员：全部属性可读，且可见性不拦截；
//   - 普通主体：实例必须可见（任一携带标签给出可见允许且无可见拒绝）；
//     每个属性按 deny-overrides 合并携带标签的可读结论，
//     被拒绝的属性以 Allowed=false 返回，不附带取值；
//   - 判定过程读取的是属性真实取值，但拒绝结果不泄露取值本身，
//     且返回体不包含任何标签/依据信息。
func (s *Snapshot) ReadAttributes(subject, ot, id string, attrs []string) (*ReadResult, error) {
	e := s.platform.beginAudit("ReadAttributes", subject, ot, id, joinStrings(attrs))
	eng, res, basis, err := s.readAttributesInternal(subject, ot, id, attrs)
	s.platform.finishAuditFull(e, res, err, auditAttrs(eng), basis)
	return res, err
}

func auditAttrs(eng *engine) []string {
	if eng == nil {
		return nil
	}
	return eng.AttrsRead()
}

func (s *Snapshot) readAttributesInternal(subject, ot, id string, attrs []string) (*engine, *ReadResult, []TagVerdict, error) {
	eng, cat, _, err := s.prepare(subject, ot, id)
	if err != nil {
		return nil, nil, nil, err
	}

	admin := s.platform.isAdmin(subject)
	var carried map[string]struct{}
	var basis []TagVerdict
	if admin {
		carried, basis, err = eng.evaluateAll()
		if err != nil {
			return eng, nil, nil, mapRuleError(err)
		}
	} else {
		reqAttrs := attrs
		carried, basis, err = eng.evaluateTargets(relevantTags(cat, subject, reqAttrs, true))
		if err != nil {
			return eng, nil, nil, mapRuleError(err)
		}
	}
	if !admin {
		vis := decideAttr(cat, subject, carried, "", grantVisibility)
		if !vis.Allowed {
			return eng, nil, basis, ErrNotVisible
		}
	}

	reqAttrs := attrs
	if len(reqAttrs) == 0 {
		reqAttrs = sortedKeysOfType(cat, ot)
	}

	result := &ReadResult{Version: s.version, Attrs: map[string]AttrResult{}}
	for _, attr := range reqAttrs {
		_, ok := cat.types[ot].attrs[attr]
		if !ok {
			return eng, nil, basis, &Error{Code: CodeUnknownAttribute, Msg: "unknown attribute: " + attr}
		}
		if admin {
			val := eng.readAttr(attr)
			result.Attrs[attr] = AttrResult{Allowed: true, Present: true, Value: val}
			continue
		}
		d := decideAttr(cat, subject, carried, attr, grantRead)
		if !d.Allowed {
			// 不读取、不返回取值；错误信息也不含任何取值内容。
			result.Attrs[attr] = AttrResult{Allowed: false}
			continue
		}
		val := eng.readAttr(attr)
		result.Attrs[attr] = AttrResult{Allowed: true, Present: true, Value: val}
	}
	return eng, result, basis, nil
}

// TagCarried 供管理员在快照内查询某实例是否携带某标签（管理/审计用途）。
// 普通主体不允许直接获知标签状态，避免其借此推断不可读属性的取值。
func (s *Snapshot) TagCarried(subject, ot, id, tag string) (bool, error) {
	e := s.platform.beginAudit("TagCarried", subject, ot, id, tag)
	var out bool
	var err error
	defer func() { s.platform.finishAudit(e, nil, err) }()
	if !s.platform.isAdmin(subject) {
		err = ErrNotAdmin
		return false, err
	}
	eng, _, _, err := s.prepare(subject, ot, id)
	if err != nil {
		return false, err
	}
	if _, ok := eng.rules[tag]; !ok {
		err = ErrUnknownTag
		return false, err
	}
	if err = eng.evalFor(tag); err != nil {
		err = mapRuleError(err)
		return false, err
	}
	out = eng.carried[tag]
	return out, nil
}

// Explain 供管理员获取一次读取的完整裁决依据（标签携带状态、属性读取集合），
// 用于审计与日志，不向普通主体开放。
type Explanation struct {
	Result    *ReadResult
	Version   int64
	AttrsRead []string
	Basis     []TagVerdict
}

func (s *Snapshot) Explain(subject, ot, id string, attrs []string) (*Explanation, error) {
	e := s.platform.beginAudit("Explain", subject, ot, id, joinStrings(attrs))
	var exp *Explanation
	var err error
	defer func() { s.platform.finishAudit(e, exp, err) }()
	if !s.platform.isAdmin(subject) {
		err = ErrNotAdmin
		return nil, err
	}
	var eng2 *engine
	var res *ReadResult
	var basis2 []TagVerdict
	eng2, res, basis2, err = s.readAttributesInternal(subject, ot, id, attrs)
	if err != nil {
		return nil, err
	}
	exp = &Explanation{
		Result:    res,
		Version:   s.version,
		AttrsRead: eng2.AttrsRead(),
		Basis:     basis2,
	}
	return exp, nil
}

func sortedKeysOfType(cat *catalogState, ot string) []string {
	out := make([]string, 0, len(cat.types[ot].attrs))
	for a := range cat.types[ot].attrs {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
