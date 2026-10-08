package authz

import "strings"

// matchElement reports whether value matches one set element. Elements use
// four forms: an exact string, a prefix pattern ending in '*', a suffix
// pattern starting with '*', or a lone '*' matching everything. Validity of
// the form itself is enforced by set validation, not here.
func matchElement(element, value string) bool {
	if element == "*" {
		return true
	}
	if strings.HasPrefix(element, "*") {
		return strings.HasSuffix(value, element[1:])
	}
	if strings.HasSuffix(element, "*") {
		return strings.HasPrefix(value, element[:len(element)-1])
	}
	return element == value
}

// inPatternSet reports whether value matches any element of set (OR).
func inPatternSet(set []string, value string) bool {
	for _, e := range set {
		if matchElement(e, value) {
			return true
		}
	}
	return false
}

func inExactSet(set []string, value string) bool {
	for _, e := range set {
		if e == value {
			return true
		}
	}
	return false
}

func inPortSet(set []int, port int) bool {
	for _, p := range set {
		if p == port {
			return true
		}
	}
	return false
}

// selectorSatisfied: every selector key/value must equal the target label.
func selectorSatisfied(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// matchSource: negation sets veto first, then positive sets (when non-empty)
// must contain the value.
func matchSource(spec *SourceSpec, req *Request) bool {
	if spec == nil {
		return true
	}
	if len(spec.NotIdentities) > 0 && inPatternSet(spec.NotIdentities, req.SourceIdentity) {
		return false
	}
	if len(spec.Identities) > 0 && !inPatternSet(spec.Identities, req.SourceIdentity) {
		return false
	}
	if len(spec.NotNamespaces) > 0 && inPatternSet(spec.NotNamespaces, req.SourceNamespace) {
		return false
	}
	if len(spec.Namespaces) > 0 && !inPatternSet(spec.Namespaces, req.SourceNamespace) {
		return false
	}
	return true
}

// matchOperation: methods compare exactly (case-sensitive); paths use the
// pattern forms; ports compare as integers.
func matchOperation(spec *OperationSpec, req *Request) bool {
	if spec == nil {
		return true
	}
	if len(spec.NotMethods) > 0 && inExactSet(spec.NotMethods, req.Method) {
		return false
	}
	if len(spec.Methods) > 0 && !inExactSet(spec.Methods, req.Method) {
		return false
	}
	if len(spec.NotPaths) > 0 && inPatternSet(spec.NotPaths, req.Path) {
		return false
	}
	if len(spec.Paths) > 0 && !inPatternSet(spec.Paths, req.Path) {
		return false
	}
	if len(spec.NotPorts) > 0 && inPortSet(spec.NotPorts, req.Port) {
		return false
	}
	if len(spec.Ports) > 0 && !inPortSet(spec.Ports, req.Port) {
		return false
	}
	return true
}

// matchConditions: every condition must hold. Header names are
// case-insensitive; values compare exactly. A missing header fails a
// condition that requires values, but satisfies one that only negates.
// With a multi-value header, a positive set is satisfied by any one value,
// and a negation set is violated by any one value.
func matchConditions(conds []Condition, headers map[string][]string) bool {
	for _, c := range conds {
		values := headers[strings.ToLower(c.Header)]
		if len(values) == 0 {
			if len(c.Values) > 0 {
				return false
			}
			continue
		}
		if len(c.Values) > 0 {
			found := false
			for _, v := range values {
				if inExactSet(c.Values, v) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		for _, v := range values {
			if inExactSet(c.NotValues, v) {
				return false
			}
		}
	}
	return true
}

// ruleMatches: conjunction of the three parts; absent parts impose nothing.
func ruleMatches(r *Rule, req *Request, headers map[string][]string) bool {
	return matchSource(r.Source, req) &&
		matchOperation(r.Operation, req) &&
		matchConditions(r.Conditions, headers)
}

// policyHit: a policy hits iff at least one rule matches. An empty rule
// list never hits.
func policyHit(p *Policy, req *Request, headers map[string][]string) bool {
	for i := range p.Rules {
		if ruleMatches(&p.Rules[i], req, headers) {
			return true
		}
	}
	return false
}

// applicable: namespace scope (target namespace or root namespace) plus
// selector satisfaction.
func applicable(p *Policy, req *Request, rootNamespace string) bool {
	if p.Namespace != req.TargetNamespace && p.Namespace != rootNamespace {
		return false
	}
	return selectorSatisfied(p.Selector, req.TargetLabels)
}
