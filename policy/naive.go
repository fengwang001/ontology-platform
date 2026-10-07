package policy

// NaiveArbiter is an independently maintained reference implementation. It
// deliberately uses different data structures and iteration style than the
// production engine: it linearly scans every policy, builds decisions in
// attribute-name order, and memoizes derivations with explicit recursion.
// Differential tests assert Render and NaiveRender agree on every generated
// policy set and instance, including the distinct error classes.

import "sort"

// NaiveOutcome is the reference result. ErrorKind mirrors Kind (0 = success).
type NaiveOutcome struct {
	ErrorKind       Kind
	ErrorCycle      []string
	Presented       map[string]any
	AttributeErrors []AttributeError
}

// NaiveRender evaluates a presentation request using the naive algorithm.
func NaiveRender(set PolicySet, inst Instance, subject string) NaiveOutcome {
	var t *ObjectType
	for i := range set.Types {
		if set.Types[i].Name == inst.Type {
			t = &set.Types[i]
			break
		}
	}
	if t == nil {
		return NaiveOutcome{ErrorKind: KindMissingReference}
	}

	vis := []VisibilityPolicy{}
	mask := []MaskingPolicy{}
	for _, p := range set.Visibility {
		if p.Object == inst.Type && p.Subject == subject {
			vis = append(vis, p)
		}
	}
	for _, p := range set.Masking {
		if p.Object == inst.Type && p.Subject == subject {
			mask = append(mask, p)
		}
	}
	sort.Slice(vis, func(a, b int) bool { return vis[a].ID < vis[b].ID })
	sort.Slice(mask, func(a, b int) bool { return mask[a].ID < mask[b].ID })

	attrNames := make([]string, 0, len(t.Attrs))
	for name := range t.Attrs {
		attrNames = append(attrNames, name)
	}
	sort.Strings(attrNames)
	known := func(a string) bool { _, ok := t.Attrs[a]; return ok }

	// Phase 1: references.
	for _, p := range vis {
		if !known(p.Attr) {
			return NaiveOutcome{ErrorKind: KindMissingReference}
		}
		if p.Pred != nil && !known(p.Pred.CondAttr) {
			return NaiveOutcome{ErrorKind: KindMissingReference}
		}
	}
	for _, p := range mask {
		if !known(p.Attr) {
			return NaiveOutcome{ErrorKind: KindMissingReference}
		}
		if p.Rule.Kind == RuleCopyDerived && !known(p.Rule.SourceAttr) {
			return NaiveOutcome{ErrorKind: KindMissingReference}
		}
	}

	// Phase 2: visibility on raw values.
	decision := map[string]string{}
	for _, attr := range attrNames {
		allow, deny := false, false
		for _, p := range vis {
			if p.Attr != attr {
				continue
			}
			if p.Pred != nil && !evalPred(p.Pred, inst.Values[p.Pred.CondAttr]) {
				continue
			}
			if p.Allow {
				allow = true
			} else {
				deny = true
			}
		}
		if allow && deny {
			return NaiveOutcome{ErrorKind: KindVisibilityConflict}
		}
		if allow {
			decision[attr] = "allow"
		} else {
			decision[attr] = "deny"
		}
	}

	// Merge masking independently per attribute.
	final := map[string]MaskingPolicy{}
	for _, attr := range attrNames {
		if decision[attr] != "allow" {
			continue
		}
		var best *MaskingPolicy
		bestStrength := StrengthNone
		for j := range mask {
			p := mask[j]
			if p.Attr != attr {
				continue
			}
			if p.Strength > bestStrength {
				bestStrength = p.Strength
				best = &mask[j]
			} else if p.Strength == bestStrength && best != nil {
				pr, br := ruleProtectiveRank(p.Rule.Kind), ruleProtectiveRank(best.Rule.Kind)
				if pr < br || (pr == br && p.ID < best.ID) {
					best = &mask[j]
				}
			}
		}
		if best != nil {
			final[attr] = *best
		}
	}

	// Phase 3: cycle detection with iterative three-color DFS (different style
	// than the engine's recursive DFS).
	if cyc := naiveCycle(attrNames, final); cyc != nil {
		return NaiveOutcome{ErrorKind: KindMaskingCycle, ErrorCycle: cyc}
	}

	// Phase 4: derive with memoization and explicit error isolation.
	out := NaiveOutcome{Presented: map[string]any{}}
	done := map[string]int{} // 0 pending,1 working,2 done
	var derive func(string)
	derive = func(attr string) {
		if done[attr] == 2 {
			return
		}
		done[attr] = 1
		p, masked := final[attr]
		if masked && p.Rule.Kind == RuleCopyDerived {
			src := p.Rule.SourceAttr
			if decision[src] == "allow" && done[src] == 0 {
				derive(src)
			}
		}
		var val any
		if !masked {
			val = inst.Values[attr]
		} else {
			val = applyRuleNaive(p.Rule, inst.Values[attr], out.Presented)
		}
		if err := checkValue(t.Attrs[attr], val); err != nil {
			out.AttributeErrors = append(out.AttributeErrors, AttributeError{Attr: attr, Err: &Error{Kind: KindTypeViolation}})
		} else {
			out.Presented[attr] = val
		}
		done[attr] = 2
	}
	for _, attr := range attrNames {
		if decision[attr] == "allow" {
			derive(attr)
		}
	}
	sort.Slice(out.AttributeErrors, func(a, b int) bool { return out.AttributeErrors[a].Attr < out.AttributeErrors[b].Attr })
	return out
}

// naiveCycle finds a cycle by repeatedly removing nodes with no outgoing edge
// (Kahn-style reduction), then canonicalizes any remaining cycle. This is an
// intentionally different algorithm than detectCycle.
func naiveCycle(nodes []string, final map[string]MaskingPolicy) []string {
	out := map[string]string{}
	for attr, p := range final {
		if p.Rule.Kind == RuleCopyDerived {
			out[attr] = p.Rule.SourceAttr
		}
	}
	alive := map[string]bool{}
	for _, n := range nodes {
		if _, ok := out[n]; ok {
			alive[n] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for n := range alive {
			dst := out[n]
			if !alive[dst] {
				delete(alive, n)
				changed = true
				break
			}
		}
	}
	if len(alive) == 0 {
		return nil
	}
	start := ""
	for _, n := range nodes {
		if alive[n] {
			start = n
			break
		}
	}
	cyc := []string{start}
	seen := map[string]bool{start: true}
	cur := out[start]
	for !seen[cur] {
		seen[cur] = true
		cyc = append(cyc, cur)
		cur = out[cur]
	}
	cyc = append(cyc, cur)
	return canonicalCycle(cyc)
}

// applyRuleNaive mirrors the production rules without sharing the switch, to
// maximize implementation independence while preserving semantics.
func applyRuleNaive(rule DerivedRule, raw any, presented map[string]any) any {
	if rule.Kind == RuleRedact {
		return redactedSentinel
	}
	if rule.Kind == RuleHash {
		return stableHash(raw)
	}
	if rule.Kind == RuleMask {
		return maskString(raw, rule.KeepRunes)
	}
	if rule.Kind == RuleConst {
		return rule.ConstVal
	}
	if v, ok := presented[rule.SourceAttr]; ok {
		return v
	}
	return nil
}
