package policy

// Engine evaluates presentation requests against a registry snapshot.

import (
	"fmt"
	"sort"
)

// predOps is the set of supported raw-value predicate operators.
var predOps = map[string]bool{"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true}

// AttrBasis records exactly which policies drove the final decision for an
// attribute, giving reproducible arbitration evidence in the audit log.
type AttrBasis struct {
	Decision          string
	VisibilityMatched []string
	MaskingMerged     []string
	FinalStrength     Strength
	FinalRulePolicy   string
	FinalRule         RuleKind
}

// Result is the final, reproducible presentation of one instance to one
// subject. Denied attributes and attributes with type violations are absent
// from Presented.
type Result struct {
	Revision        int64
	Object          string
	Instance        string
	Subject         string
	Presented       map[string]any
	Basis           map[string]AttrBasis
	AttributeErrors []AttributeError
}

// Render produces the final presentation of inst to subject.
//
// Evaluation proceeds in four phases; the first failing phase aborts the
// whole request with a typed *Error and, per the specification, leaves no
// audit record:
//  1. MissingReference   - unknown object/attribute/rule input
//  2. VisibilityConflict - simultaneous matching allow and deny
//  3. MaskingCycle       - cyclic copy-derived dependencies
//  4. TypeViolation      - per attribute; never aborts other attributes
//
// Visibility predicates are always evaluated on raw instance values; raw
// values are never placed into Presented when a masking policy applies.
func (r *Registry) Render(inst Instance, subject string) (*Result, error) {
	snap := r.currentSnap()

	objType, ok := snap.types[inst.Type]
	if !ok {
		return nil, newMissingReference("object type %q is not registered", inst.Type)
	}

	visPolicies := snap.visOS(inst.Type, subject)
	maskPolicies := snap.maskOS(inst.Type, subject)
	r.visExamined.Add(int64(len(visPolicies)))
	r.maskExamined.Add(int64(len(maskPolicies)))

	if err := validateReferences(objType, visPolicies, maskPolicies); err != nil {
		return nil, err
	}

	decisions := make(map[string]string, len(objType.Attrs))
	basis := make(map[string]AttrBasis, len(objType.Attrs))
	if err := decideVisibility(objType, inst, subject, visPolicies, decisions, basis); err != nil {
		return nil, err
	}

	finalMask := map[string]*MaskingPolicy{}
	maskGroups := map[string][]*MaskingPolicy{}
	for _, p := range maskPolicies {
		if decisions[p.Attr] == "allow" {
			maskGroups[p.Attr] = append(maskGroups[p.Attr], p)
		}
	}
	for attr, group := range maskGroups {
		chosen, strength := mergeMasking(group)
		finalMask[attr] = chosen
		b := basis[attr]
		b.MaskingMerged = policyIDs(group)
		b.FinalStrength = strength
		b.FinalRulePolicy = chosen.ID
		b.FinalRule = chosen.Rule.Kind
		basis[attr] = b
	}

	if cyc := detectCycle(objType, finalMask); cyc != nil {
		return nil, &Error{Kind: KindMaskingCycle, Msg: "masking derivation forms a cycle", Cycle: cyc}
	}

	res := &Result{
		Revision:  snap.revision,
		Object:    inst.Type,
		Instance:  inst.ID,
		Subject:   subject,
		Presented: map[string]any{},
		Basis:     basis,
	}
	deriveAll(objType, inst, decisions, finalMask, res)

	r.audit.Append(inst, subject, res)
	return res, nil
}

// validateReferences implements phase 1. Policies are already sorted by id, so
// the first failure is unique and registration-order independent.
func validateReferences(t ObjectType, vis []*VisibilityPolicy, mask []*MaskingPolicy) error {
	attrKnown := func(name string) bool { _, ok := t.Attrs[name]; return ok }
	for _, p := range vis {
		if !attrKnown(p.Attr) {
			return newMissingReference("visibility policy %q references unknown attribute %q of %q", p.ID, p.Attr, t.Name)
		}
		if p.Pred != nil && !attrKnown(p.Pred.CondAttr) {
			return newMissingReference("visibility policy %q references unknown condition attribute %q of %q", p.ID, p.Pred.CondAttr, t.Name)
		}
	}
	for _, p := range mask {
		if !attrKnown(p.Attr) {
			return newMissingReference("masking policy %q references unknown attribute %q of %q", p.ID, p.Attr, t.Name)
		}
		if p.Rule.Kind == RuleCopyDerived && !attrKnown(p.Rule.SourceAttr) {
			return newMissingReference("masking policy %q copy-references unknown attribute %q of %q", p.ID, p.Rule.SourceAttr, t.Name)
		}
	}
	return nil
}

// decideVisibility implements phase 2 using raw values only.
func decideVisibility(t ObjectType, inst Instance, subject string, vis []*VisibilityPolicy, decisions map[string]string, basis map[string]AttrBasis) error {
	allows := map[string][]string{}
	denies := map[string][]string{}
	for _, p := range vis {
		if p.Pred != nil && !evalPred(p.Pred, inst.Values[p.Pred.CondAttr]) {
			continue
		}
		if p.Allow {
			allows[p.Attr] = append(allows[p.Attr], p.ID)
		} else {
			denies[p.Attr] = append(denies[p.Attr], p.ID)
		}
	}
	for _, attr := range sortedAttrs(t.Attrs) {
		allowIDs, denyIDs := allows[attr], denies[attr]
		switch {
		case len(allowIDs) > 0 && len(denyIDs) > 0:
			return newVisibilityConflict("attribute %q has both allow %v and deny %v decisions for subject %q",
				attr, allowIDs, denyIDs, subject)
		case len(allowIDs) > 0:
			decisions[attr] = "allow"
			basis[attr] = AttrBasis{Decision: "allow", VisibilityMatched: allowIDs}
		default:
			// Default-deny: with no explicit allow an attribute is hidden.
			decisions[attr] = "deny"
			basis[attr] = AttrBasis{Decision: "deny", VisibilityMatched: denyIDs}
		}
	}
	return nil
}

// mergeMasking merges all masking policies hit by one subject on one attribute.
// The unique final strength is the maximum declared strength. If several
// policies share that strength, the most protective rule kind wins under a
// fixed ordering; remaining ties resolve to the lexicographically smallest
// policy id. The result is therefore independent of registration order.
func mergeMasking(group []*MaskingPolicy) (*MaskingPolicy, Strength) {
	maxStrength := StrengthNone
	for _, p := range group {
		if p.Strength > maxStrength {
			maxStrength = p.Strength
		}
	}
	var chosen *MaskingPolicy
	for _, p := range group {
		if p.Strength != maxStrength {
			continue
		}
		if chosen == nil ||
			ruleProtectiveRank(p.Rule.Kind) < ruleProtectiveRank(chosen.Rule.Kind) ||
			(ruleProtectiveRank(p.Rule.Kind) == ruleProtectiveRank(chosen.Rule.Kind) && p.ID < chosen.ID) {
			chosen = p
		}
	}
	return chosen, maxStrength
}

// ruleProtectiveRank orders rule kinds from most protective (smallest rank)
// to least protective for deterministic same-strength merging.
func ruleProtectiveRank(k RuleKind) int {
	switch k {
	case RuleRedact:
		return 0
	case RuleHash:
		return 1
	case RuleMask:
		return 2
	case RuleConst:
		return 3
	default: // RuleCopyDerived
		return 4
	}
}

// detectCycle returns a deterministic cycle among final copy-derived rules,
// canonicalized to start at its smallest attribute.
func detectCycle(t ObjectType, finalMask map[string]*MaskingPolicy) []string {
	edges := map[string][]string{}
	for attr, p := range finalMask {
		if p.Rule.Kind == RuleCopyDerived {
			edges[attr] = append(edges[attr], p.Rule.SourceAttr)
		}
	}
	for k := range edges {
		sort.Strings(edges[k])
	}
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	stack := []string{}
	var found []string
	var dfs func(node string) bool
	dfs = func(node string) bool {
		color[node] = gray
		stack = append(stack, node)
		for _, next := range edges[node] {
			if color[next] == black {
				continue
			}
			if color[next] == gray {
				var cyc []string
				for i := len(stack) - 1; i >= 0; i-- {
					cyc = append([]string{stack[i]}, cyc...)
					if stack[i] == next {
						break
					}
				}
				cyc = append(cyc, next)
				found = canonicalCycle(cyc)
				return true
			}
			if dfs(next) {
				return true
			}
		}
		color[node] = black
		stack = stack[:len(stack)-1]
		return false
	}
	for _, attr := range sortedAttrs(t.Attrs) {
		if color[attr] == white {
			if dfs(attr) {
				return found
			}
		}
	}
	return nil
}

// canonicalCycle rotates a closed node path so it starts at its smallest
// attribute, yielding a unique representation regardless of DFS orientation.
func canonicalCycle(cyc []string) []string {
	if len(cyc) <= 1 {
		return cyc
	}
	nodes := cyc[:len(cyc)-1]
	minIdx := 0
	for i, name := range nodes {
		if name < nodes[minIdx] {
			minIdx = i
		}
	}
	out := make([]string, 0, len(nodes)+1)
	out = append(out, nodes[minIdx:]...)
	out = append(out, nodes[:minIdx]...)
	out = append(out, nodes[minIdx])
	return out
}

// deriveAll computes final values in dependency order. Copy-derived inputs are
// read from presented (already derived) values, never from raw values.
func deriveAll(t ObjectType, inst Instance, decisions map[string]string, finalMask map[string]*MaskingPolicy, res *Result) {
	presented := res.Presented
	state := map[string]int{} // 0 pending, 1 in progress, 2 done
	for attr := range decisions {
		if decisions[attr] == "allow" {
			state[attr] = 0
		}
	}
	var derive func(attr string)
	derive = func(attr string) {
		if decisions[attr] != "allow" || state[attr] != 0 {
			return // denied attributes never derive; cycles are rejected in phase 3
		}
		state[attr] = 1
		p := finalMask[attr]
		if p != nil && p.Rule.Kind == RuleCopyDerived {
			src := p.Rule.SourceAttr
			if decisions[src] == "allow" {
				derive(src)
			}
		}
		raw := inst.Values[attr]
		var derived any
		if p == nil {
			derived = raw
		} else {
			derived = applyRule(p.Rule, raw, presented)
		}
		ty := t.Attrs[attr]
		if err := checkValue(ty, derived); err != nil {
			res.AttributeErrors = append(res.AttributeErrors, AttributeError{
				Attr: attr,
				Err:  &Error{Kind: KindTypeViolation, Msg: fmt.Sprintf("derived value for %q violates declared type %s: %s", attr, ty.Kind, err.Error())},
			})
			state[attr] = 2
			return
		}
		presented[attr] = derived
		state[attr] = 2
	}
	for _, attr := range sortedAttrs(t.Attrs) {
		derive(attr)
	}
}

// applyRule computes the derived value. Copy-derived reads the source's final
// presented value (nil when denied, unset, or itself failing type validation).
func applyRule(rule DerivedRule, raw any, presented map[string]any) any {
	switch rule.Kind {
	case RuleRedact:
		return redactedSentinel
	case RuleHash:
		return stableHash(raw)
	case RuleMask:
		return maskString(raw, rule.KeepRunes)
	case RuleConst:
		return rule.ConstVal
	case RuleCopyDerived:
		v, ok := presented[rule.SourceAttr]
		if !ok {
			return nil
		}
		return v
	default:
		return nil
	}
}

// checkValue validates one value against its declared attribute type.
func checkValue(ty AttrType, v any) error {
	if v == nil {
		return fmt.Errorf("value is absent")
	}
	var num float64
	switch ty.Kind {
	case KindString:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", v)
		}
		if ty.MaxLen > 0 && len([]rune(s)) > ty.MaxLen {
			return fmt.Errorf("string length %d exceeds MaxLen %d", len([]rune(s)), ty.MaxLen)
		}
		if !allowedValue(ty, s) {
			return fmt.Errorf("value %v not in allowed set", s)
		}
		return nil
	case KindInt:
		n, ok := intValue(v)
		if !ok {
			return fmt.Errorf("expected int, got %T", v)
		}
		num = float64(n)
	case KindFloat:
		f, ok := v.(float64)
		if !ok {
			return fmt.Errorf("expected float64, got %T", v)
		}
		num = f
	case KindBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected bool, got %T", v)
		}
		if !allowedValue(ty, v) {
			return fmt.Errorf("value %v not in allowed set", v)
		}
		return nil
	default:
		return fmt.Errorf("unknown attribute kind")
	}
	if err := checkRange(ty, num); err != nil {
		return err
	}
	if !allowedValue(ty, num) {
		return fmt.Errorf("value %v not in allowed set", num)
	}
	return nil
}

func checkRange(ty AttrType, f float64) error {
	if ty.Min != nil && f < *ty.Min {
		return fmt.Errorf("value %v below minimum %v", f, *ty.Min)
	}
	if ty.Max != nil && f > *ty.Max {
		return fmt.Errorf("value %v above maximum %v", f, *ty.Max)
	}
	return nil
}

func allowedValue(ty AttrType, v any) bool {
	if len(ty.Allowed) == 0 {
		return true
	}
	for _, a := range ty.Allowed {
		if a == v {
			return true
		}
	}
	return false
}

// intValue accepts int variants used by callers constructing instances.
func intValue(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	default:
		return 0, false
	}
}

// evalPred evaluates one predicate against a raw attribute value.
func evalPred(p *Predicate, raw any) bool {
	cmp, comparable := compareValues(raw, p.Value)
	switch p.Op {
	case "eq":
		return valuesEqual(raw, p.Value)
	case "ne":
		return !valuesEqual(raw, p.Value)
	case "lt", "le", "gt", "ge":
		if !comparable {
			return false
		}
		switch p.Op {
		case "lt":
			return cmp < 0
		case "le":
			return cmp <= 0
		case "gt":
			return cmp > 0
		default:
			return cmp >= 0
		}
	default:
		return false
	}
}

func valuesEqual(a, b any) bool {
	if an, ok1 := intValue(a); ok1 {
		if bn, ok2 := intValue(b); ok2 {
			return an == bn
		}
	}
	return a == b
}

// compareValues returns -1/0/1 when both values are orderable numerically or
// as strings; comparable=false otherwise.
func compareValues(a, b any) (int, bool) {
	if af, aok := floatOf(a); aok {
		if bf, bok := floatOf(b); bok {
			switch {
			case af < bf:
				return -1, true
			case af > bf:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	if as, ok1 := a.(string); ok1 {
		if bs, ok2 := b.(string); ok2 {
			switch {
			case as < bs:
				return -1, true
			case as > bs:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	return 0, false
}

func floatOf(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func policyIDs(list []*MaskingPolicy) []string {
	ids := make([]string, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}
