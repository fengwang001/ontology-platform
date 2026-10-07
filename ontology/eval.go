package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

func sortStrings(s []string) { sort.Strings(s) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type derivedEntry struct {
	value     Value
	policyIDs []string
	err       *ClassifiedError
}

type evaluator struct {
	snap         *snapshot
	subject      string
	inst         Instance
	defaultAllow bool
	examined     int
	memo         map[string]derivedEntry
	onStack      map[string]bool
	errors       []ClassifiedError
}

func (ev *evaluator) run() Result {
	res := Result{
		Values:  map[string]Value{},
		Outcome: map[string]AttrOutcome{},
	}
	// 只考察实例自身的属性：开销与系统中策略总数无关。
	for _, attr := range sortedKeys(ev.inst.Attrs) {
		out := ev.presentAttr(attr)
		res.Outcome[attr] = out
		if out.Present {
			res.Values[attr] = out.Value
		}
	}
	res.Errors = dedupeSortErrors(ev.errors)
	res.Examined = ev.examined
	return res
}

func dedupeSortErrors(errs []ClassifiedError) []ClassifiedError {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Kind.rank() != errs[j].Kind.rank() {
			return errs[i].Kind.rank() < errs[j].Kind.rank()
		}
		if errs[i].Attr != errs[j].Attr {
			return errs[i].Attr < errs[j].Attr
		}
		return errs[i].Detail < errs[j].Detail
	})
	out := errs[:0]
	var prev ClassifiedError
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

// presentAttr 裁决单个属性的最终呈现。
func (ev *evaluator) presentAttr(attr string) AttrOutcome {
	typ, declared := ev.snap.schema[attr]
	raw := ev.inst.Attrs[attr]
	if declared {
		if err := typ.Check(raw); err != nil {
			ev.addErr(ClassifiedError{Kind: ErrTypeViolation, Attr: attr,
				Detail: fmt.Sprintf("实例原始值违反声明类型: %v", err)})
			return AttrOutcome{}
		}
	}
	if !declared && ev.hasPolicies(attr) {
		ev.addErr(ClassifiedError{Kind: ErrMissingRef, Attr: attr,
			Detail: "策略引用的属性未在 schema 中声明"})
		return AttrOutcome{}
	}

	vis := ev.resolveVisibility(attr)
	if vis.err != nil {
		ev.addErr(*vis.err)
		return AttrOutcome{PolicyIDs: vis.policyIDs}
	}
	if !vis.visible {
		return AttrOutcome{PolicyIDs: vis.policyIDs}
	}

	derived := ev.derive(attr, raw)
	if derived.err != nil {
		ev.addErr(*derived.err)
		return AttrOutcome{PolicyIDs: appendIDs(vis.policyIDs, derived.policyIDs)}
	}
	ids := appendIDs(vis.policyIDs, derived.policyIDs)
	return AttrOutcome{Present: true, Value: derived.value, PolicyIDs: ids}
}

func (ev *evaluator) hasPolicies(attr string) bool {
	return len(ev.snap.visByAttr[attr]) > 0 || len(ev.snap.maskByAttr[attr]) > 0
}

type visDecision struct {
	visible   bool
	policyIDs []string
	err       *ClassifiedError
}

// resolveVisibility 合并命中的可见性策略；判定条件一律基于原始值。
func (ev *evaluator) resolveVisibility(attr string) visDecision {
	ids := ev.snap.visByAttr[attr]
	var matched []VisibilityPolicy
	for _, id := range ids {
		p := ev.snap.vis[id]
		ev.examined++
		if !subjectMatch(p.Subject, ev.subject) {
			continue
		}
		matched = append(matched, p)
	}
	if len(matched) == 0 {
		return visDecision{visible: ev.defaultAllow}
	}
	var active []VisibilityPolicy
	for _, p := range matched {
		ok, cerr := ev.evalCondition(p)
		if cerr != nil {
			return visDecision{err: cerr}
		}
		if ok {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return visDecision{visible: ev.defaultAllow, policyIDs: policyIDsOf(matched)}
	}
	hasAllow, hasDeny := false, false
	for _, p := range active {
		if p.Effect == Allow {
			hasAllow = true
		} else {
			hasDeny = true
		}
	}
	activeIDs := policyIDsOf(active)
	if hasAllow && hasDeny {
		return visDecision{policyIDs: activeIDs, err: &ClassifiedError{
			Kind: ErrVisibilityConflict, Attr: attr, Policies: activeIDs,
			Detail: "同一属性同时命中允许与拒绝策略，无法调和",
		}}
	}
	return visDecision{visible: hasAllow, policyIDs: activeIDs}
}

// evalCondition 在原始值上求值可见性条件，不触碰任何派生值。
func (ev *evaluator) evalCondition(p VisibilityPolicy) (bool, *ClassifiedError) {
	if p.Cond == nil {
		return true, nil
	}
	raw, ok := ev.inst.Attrs[p.Cond.Attr]
	if !ok {
		return false, &ClassifiedError{
			Kind: ErrMissingRef, Attr: p.Cond.Attr, Policies: []string{p.ID},
			Detail: fmt.Sprintf("可见性策略 %s 的条件引用了不存在的属性", p.ID),
		}
	}
	eq := valuesEqual(raw, p.Cond.Value)
	if p.Cond.Op == OpNeq {
		return !eq, nil
	}
	return eq, nil
}

func valuesEqual(a, b Value) bool { return a.V == b.V }

// derive 计算属性的脱敏派生值；带记忆化与在栈检测，循环确定可报。
func (ev *evaluator) derive(attr string, raw Value) derivedEntry {
	if ent, ok := ev.memo[attr]; ok {
		return ent
	}
	if ev.onStack[attr] {
		return derivedEntry{err: &ClassifiedError{
			Kind: ErrMaskingCycle, Attr: attr,
			Detail: "脱敏依赖关系构成循环",
		}}
	}
	pol, ids := ev.mergeMasking(attr)
	if pol == nil {
		ent := derivedEntry{value: raw}
		ev.memo[attr] = ent
		return ent
	}
	ev.onStack[attr] = true
	defer delete(ev.onStack, attr)

	val, derr := ev.applyRule(attr, pol.Rule, raw)
	if derr != nil {
		derr.Policies = append([]string{pol.ID}, derr.Policies...)
		ent := derivedEntry{policyIDs: ids, err: derr}
		ev.memo[attr] = ent
		return ent
	}
	if typ, declared := ev.snap.schema[attr]; declared {
		if err := typ.Check(val); err != nil {
			ent := derivedEntry{policyIDs: ids, err: &ClassifiedError{
				Kind: ErrTypeViolation, Attr: attr, Policies: ids,
				Detail: fmt.Sprintf("脱敏派生值违反声明类型: %v", err),
			}}
			ev.memo[attr] = ent
			return ent
		}
	}
	ent := derivedEntry{value: val, policyIDs: ids}
	ev.memo[attr] = ent
	return ent
}

// mergeMasking 按强度合并命中的脱敏策略：强度最大者胜出；
// 同强度取策略 ID 字典序最小者，结果与登记顺序无关。
func (ev *evaluator) mergeMasking(attr string) (*MaskingPolicy, []string) {
	ids := ev.snap.maskByAttr[attr]
	var matched []MaskingPolicy
	for _, id := range ids {
		p := ev.snap.mask[id]
		ev.examined++
		if subjectMatch(p.Subject, ev.subject) {
			matched = append(matched, p)
		}
	}
	if len(matched) == 0 {
		return nil, nil
	}
	best := matched[0]
	for _, p := range matched[1:] {
		if p.Strength > best.Strength ||
			(p.Strength == best.Strength && p.ID < best.ID) {
			best = p
		}
	}
	return &best, policyIDsOf(matched)
}

func (ev *evaluator) applyRule(attr string, r MaskingRule, raw Value) (Value, *ClassifiedError) {
	switch r.Kind {
	case RuleRedact:
		return StringValue("***"), nil
	case RuleHash:
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", raw.V)))
		return StringValue(hex.EncodeToString(sum[:])[:8]), nil
	case RuleTruncate:
		s, ok := raw.V.(string)
		if !ok {
			return Value{}, &ClassifiedError{Kind: ErrTypeViolation, Attr: attr,
				Detail: "truncate 规则要求原始值为 string"}
		}
		n := r.Param
		if n < 0 {
			n = 0
		}
		if n > len(s) {
			n = len(s)
		}
		return StringValue(s[:n]), nil
	case RuleConstant:
		return r.ParamValue, nil
	case RuleFromAttr:
		depRaw, ok := ev.inst.Attrs[r.InputAttr]
		if !ok {
			return Value{}, &ClassifiedError{Kind: ErrMissingRef, Attr: r.InputAttr,
				Detail: fmt.Sprintf("脱敏规则依赖的属性 %q 在实例上不存在", r.InputAttr)}
		}
		dep := ev.derive(r.InputAttr, depRaw)
		if dep.err != nil {
			cerr := *dep.err
			return Value{}, &cerr
		}
		return dep.value, nil
	}
	return Value{}, &ClassifiedError{Kind: ErrMissingRef, Attr: attr,
		Detail: "未知的派生规则类别"}
}

func (ev *evaluator) addErr(e ClassifiedError) { ev.errors = append(ev.errors, e) }

func subjectMatch(pattern, subject string) bool {
	return pattern == "*" || pattern == subject
}

func policyIDsOf[T interface{ GetID() string }](ps []T) []string {
	if len(ps) == 0 {
		return nil
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.GetID())
	}
	sort.Strings(ids)
	return ids
}

func (p VisibilityPolicy) GetID() string { return p.ID }
func (p MaskingPolicy) GetID() string    { return p.ID }

func appendIDs(a, b []string) []string {
	out := append(append([]string{}, a...), b...)
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
