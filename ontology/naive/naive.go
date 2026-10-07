// Package naive 是独立维护的朴素参照实现：
// 不使用任何索引，每次呈现都全量扫描所有已登记策略，
// 用于与主引擎做逐项差分对照。
package naive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"ontology/ontology"
)

// Engine 朴素参照引擎。
type Engine struct {
	defaultAllow bool
	schema       map[string]ontology.AttrType
	vis          []ontology.VisibilityPolicy
	mask         []ontology.MaskingPolicy
}

func New(defaultAllow bool) *Engine {
	return &Engine{schema: map[string]ontology.AttrType{}}
}

func (e *Engine) SetSchema(s map[string]ontology.AttrType) {
	e.schema = map[string]ontology.AttrType{}
	for k, v := range s {
		e.schema[k] = v
	}
}

func (e *Engine) RegisterVisibility(p ontology.VisibilityPolicy) {
	for i, q := range e.vis {
		if q.ID == p.ID {
			e.vis[i] = p
			return
		}
	}
	e.vis = append(e.vis, p)
}

func (e *Engine) RegisterMasking(p ontology.MaskingPolicy) {
	for i, q := range e.mask {
		if q.ID == p.ID {
			e.mask[i] = p
			return
		}
	}
	e.mask = append(e.mask, p)
}

func (e *Engine) Unregister(id string) {
	vis := e.vis[:0]
	for _, p := range e.vis {
		if p.ID != id {
			vis = append(vis, p)
		}
	}
	e.vis = vis
	mask := e.mask[:0]
	for _, p := range e.mask {
		if p.ID != id {
			mask = append(mask, p)
		}
	}
	e.mask = mask
}

func match(pat, sub string) bool { return pat == "*" || pat == sub }

// Present 逐属性、全量扫描地求值。
func (e *Engine) Present(subject string, inst ontology.Instance) ontology.Result {
	attrs := make([]string, 0, len(inst.Attrs))
	for a := range inst.Attrs {
		attrs = append(attrs, a)
	}
	sort.Strings(attrs)

	ev := &eval{engine: e, subject: subject, inst: inst,
		memo: map[string]derived{}, onStack: map[string]bool{}}
	res := ontology.Result{
		Values:  map[string]ontology.Value{},
		Outcome: map[string]ontology.AttrOutcome{},
	}
	for _, attr := range attrs {
		out := ev.presentAttr(attr)
		res.Outcome[attr] = out
		if out.Present {
			res.Values[attr] = out.Value
		}
	}
	res.Errors = dedupeSortErrors(ev.errors)
	return res
}

func dedupeSortErrors(errs []ontology.ClassifiedError) []ontology.ClassifiedError {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Kind != errs[j].Kind {
			return errs[i].Kind < errs[j].Kind
		}
		if errs[i].Attr != errs[j].Attr {
			return errs[i].Attr < errs[j].Attr
		}
		return errs[i].Detail < errs[j].Detail
	})
	out := errs[:0]
	var prev ontology.ClassifiedError
	for i, e := range errs {
		if i > 0 && e.Kind == prev.Kind && e.Attr == prev.Attr &&
			e.Detail == prev.Detail && fmt.Sprint(e.Policies) == fmt.Sprint(prev.Policies) {
			continue
		}
		out = append(out, e)
		prev = e
	}
	return out
}

type derived struct {
	value ontology.Value
	ids   []string
	err   *ontology.ClassifiedError
}

type eval struct {
	engine  *Engine
	subject string
	inst    ontology.Instance
	memo    map[string]derived
	onStack map[string]bool
	errors  []ontology.ClassifiedError
}

func (ev *eval) presentAttr(attr string) ontology.AttrOutcome {
	typ, declared := ev.engine.schema[attr]
	raw := ev.inst.Attrs[attr]
	if declared {
		if err := typ.Check(raw); err != nil {
			ev.errors = append(ev.errors, ontology.ClassifiedError{
				Kind: ontology.ErrTypeViolation, Attr: attr,
				Detail: fmt.Sprintf("实例原始值违反声明类型: %v", err)})
			return ontology.AttrOutcome{}
		}
	}
	hasPolicy := false
	for _, p := range ev.engine.vis {
		if p.Attr == attr {
			hasPolicy = true
		}
	}
	for _, p := range ev.engine.mask {
		if p.Attr == attr {
			hasPolicy = true
		}
	}
	if !declared && hasPolicy {
		ev.errors = append(ev.errors, ontology.ClassifiedError{
			Kind: ontology.ErrMissingRef, Attr: attr,
			Detail: "策略引用的属性未在 schema 中声明"})
		return ontology.AttrOutcome{}
	}

	var matched []ontology.VisibilityPolicy
	for _, p := range ev.engine.vis {
		if p.Attr == attr && match(p.Subject, ev.subject) {
			matched = append(matched, p)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	var active []ontology.VisibilityPolicy
	for _, p := range matched {
		ok := true
		if p.Cond != nil {
			condRaw, exists := ev.inst.Attrs[p.Cond.Attr]
			if !exists {
				ev.errors = append(ev.errors, ontology.ClassifiedError{
					Kind: ontology.ErrMissingRef, Attr: p.Cond.Attr,
					Policies: []string{p.ID},
					Detail:   fmt.Sprintf("可见性策略 %s 的条件引用了不存在的属性", p.ID)})
				return ontology.AttrOutcome{}
			}
			eq := condRaw.V == p.Cond.Value.V
			ok = eq
			if p.Cond.Op == ontology.OpNeq {
				ok = !eq
			}
		}
		if ok {
			active = append(active, p)
		}
	}
	visible := ev.engine.defaultAllow
	visIDs := idsOfVis(matched)
	if len(active) > 0 {
		hasAllow, hasDeny := false, false
		for _, p := range active {
			if p.Effect == ontology.Allow {
				hasAllow = true
			} else {
				hasDeny = true
			}
		}
		visIDs = idsOfVis(active)
		if hasAllow && hasDeny {
			ev.errors = append(ev.errors, ontology.ClassifiedError{
				Kind: ontology.ErrVisibilityConflict, Attr: attr, Policies: visIDs,
				Detail: "同一属性同时命中允许与拒绝策略，无法调和"})
			return ontology.AttrOutcome{PolicyIDs: visIDs}
		}
		visible = hasAllow
	}
	if !visible {
		return ontology.AttrOutcome{PolicyIDs: visIDs}
	}
	d := ev.derive(attr, raw)
	if d.err != nil {
		ev.errors = append(ev.errors, *d.err)
		return ontology.AttrOutcome{PolicyIDs: mergeIDs(visIDs, d.ids)}
	}
	return ontology.AttrOutcome{Present: true, Value: d.value,
		PolicyIDs: mergeIDs(visIDs, d.ids)}
}

func (ev *eval) derive(attr string, raw ontology.Value) derived {
	if ent, ok := ev.memo[attr]; ok {
		return ent
	}
	if ev.onStack[attr] {
		return derived{err: &ontology.ClassifiedError{
			Kind: ontology.ErrMaskingCycle, Attr: attr,
			Detail: "脱敏依赖关系构成循环"}}
	}
	var matched []ontology.MaskingPolicy
	for _, p := range ev.engine.mask {
		if p.Attr == attr && match(p.Subject, ev.subject) {
			matched = append(matched, p)
		}
	}
	if len(matched) == 0 {
		ent := derived{value: raw}
		ev.memo[attr] = ent
		return ent
	}
	best := matched[0]
	for _, p := range matched[1:] {
		if p.Strength > best.Strength ||
			(p.Strength == best.Strength && p.ID < best.ID) {
			best = p
		}
	}
	ids := idsOfMask(matched)
	ev.onStack[attr] = true
	defer delete(ev.onStack, attr)

	val, derr := ev.applyRule(attr, best.Rule, raw)
	if derr != nil {
		derr.Policies = append([]string{best.ID}, derr.Policies...)
		ent := derived{ids: ids, err: derr}
		ev.memo[attr] = ent
		return ent
	}
	if typ, declared := ev.engine.schema[attr]; declared {
		if err := typ.Check(val); err != nil {
			ent := derived{ids: ids, err: &ontology.ClassifiedError{
				Kind: ontology.ErrTypeViolation, Attr: attr, Policies: ids,
				Detail: fmt.Sprintf("脱敏派生值违反声明类型: %v", err)}}
			ev.memo[attr] = ent
			return ent
		}
	}
	ent := derived{value: val, ids: ids}
	ev.memo[attr] = ent
	return ent
}

func (ev *eval) applyRule(attr string, r ontology.MaskingRule, raw ontology.Value) (ontology.Value, *ontology.ClassifiedError) {
	switch r.Kind {
	case ontology.RuleRedact:
		return ontology.StringValue("***"), nil
	case ontology.RuleHash:
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", raw.V)))
		return ontology.StringValue(hex.EncodeToString(sum[:])[:8]), nil
	case ontology.RuleTruncate:
		s, ok := raw.V.(string)
		if !ok {
			return ontology.Value{}, &ontology.ClassifiedError{
				Kind: ontology.ErrTypeViolation, Attr: attr,
				Detail: "truncate 规则要求原始值为 string"}
		}
		n := r.Param
		if n < 0 {
			n = 0
		}
		if n > len(s) {
			n = len(s)
		}
		return ontology.StringValue(s[:n]), nil
	case ontology.RuleConstant:
		return r.ParamValue, nil
	case ontology.RuleFromAttr:
		depRaw, ok := ev.inst.Attrs[r.InputAttr]
		if !ok {
			return ontology.Value{}, &ontology.ClassifiedError{
				Kind: ontology.ErrMissingRef, Attr: r.InputAttr,
				Detail: fmt.Sprintf("脱敏规则依赖的属性 %q 在实例上不存在", r.InputAttr)}
		}
		dep := ev.derive(r.InputAttr, depRaw)
		if dep.err != nil {
			cerr := *dep.err
			return ontology.Value{}, &cerr
		}
		return dep.value, nil
	}
	return ontology.Value{}, &ontology.ClassifiedError{
		Kind: ontology.ErrMissingRef, Attr: attr, Detail: "未知的派生规则类别"}
}

func idsOfVis(ps []ontology.VisibilityPolicy) []string {
	if len(ps) == 0 {
		return nil
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func idsOfMask(ps []ontology.MaskingPolicy) []string {
	if len(ps) == 0 {
		return nil
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func mergeIDs(a, b []string) []string {
	out := append(append([]string{}, a...), b...)
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
