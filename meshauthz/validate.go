package meshauthz

import (
	"errors"
	"strings"
)

// Sentinel causes behind pattern compilation failures. They are
// wrapped into *Error values with the invalid-policy-set category.
var (
	errEmptyElement = errors.New("set element must not be empty")
	errStarBothEnds = errors.New("wildcard '*' at both ends is not allowed")
	errStarMiddle   = errors.New("wildcard '*' may only appear at the start or the end")
)

const (
	minPort = 1
	maxPort = 65535
)

// validateRequest checks request-field legality. Illegal requests are
// the highest-priority error category, so evaluation runs this check
// before touching any policy state.
//
// A request is legal iff: source and target namespaces are non-empty,
// the method is non-empty, the path is non-empty, the port is within
// [1, 65535], and every header name is non-empty. The source identity
// may be empty (unauthenticated); label and header values are free
// form, including empty.
func validateRequest(req *Request) error {
	if req.SourceNamespace == "" {
		return invalidArgumentf("source namespace must not be empty")
	}
	if req.TargetNamespace == "" {
		return invalidArgumentf("target namespace must not be empty")
	}
	if req.Method == "" {
		return invalidArgumentf("method must not be empty")
	}
	if req.Path == "" {
		return invalidArgumentf("path must not be empty")
	}
	if req.Port < minPort || req.Port > maxPort {
		return invalidArgumentf("port %d out of range [%d, %d]", req.Port, minPort, maxPort)
	}
	for name := range req.Headers {
		if name == "" {
			return invalidArgumentf("header name must not be empty")
		}
	}
	return nil
}

// validatePolicySet checks a full candidate policy set and reports the
// first problem in submission order. The set is rejected as a whole:
// no partial installation ever happens.
//
// Checked invariants: name and namespace non-empty; name unique within
// its namespace; action legal; selector keys non-empty; every set
// element non-empty; wildcard forms legal; ports within [1, 65535];
// condition keys non-empty.
func validatePolicySet(policies []Policy) error {
	seen := make(map[[2]string]struct{}, len(policies))
	for i := range policies {
		p := &policies[i]
		if p.Name == "" {
			return invalidPolicySetf(i, "policy name must not be empty")
		}
		if p.Namespace == "" {
			return invalidPolicySetf(i, "policy namespace must not be empty")
		}
		key := [2]string{p.Namespace, p.Name}
		if _, dup := seen[key]; dup {
			return invalidPolicySetf(i, "duplicate policy name %q in namespace %q", p.Name, p.Namespace)
		}
		seen[key] = struct{}{}
		if !p.Action.valid() {
			return invalidPolicySetf(i, "illegal action %q", string(p.Action))
		}
		for k := range p.Selector {
			if k == "" {
				return invalidPolicySetf(i, "selector key must not be empty")
			}
		}
		if _, err := compilePolicy(p, i); err != nil {
			return err
		}
	}
	return nil
}

// compilePolicy validates the rule content of one policy and compiles
// it into the immutable, evaluation-ready form. The index is only used
// for error reporting.
func compilePolicy(p *Policy, index int) (*compiledPolicy, error) {
	cp := &compiledPolicy{
		name:      p.Name,
		namespace: p.Namespace,
		action:    p.Action,
		selector:  p.Selector,
	}
	for ri := range p.Rules {
		cr, err := compileRule(&p.Rules[ri], index, ri)
		if err != nil {
			return nil, err
		}
		cp.rules = append(cp.rules, cr)
	}
	return cp, nil
}

func compileRule(r *Rule, policyIndex, ruleIndex int) (compiledRule, error) {
	var cr compiledRule
	if r.From != nil {
		from, err := compileSource(r.From, policyIndex, ruleIndex)
		if err != nil {
			return cr, err
		}
		cr.from = &from
	}
	if r.Operation != nil {
		op, err := compileOperation(r.Operation, policyIndex, ruleIndex)
		if err != nil {
			return cr, err
		}
		cr.operation = &op
	}
	for ci := range r.When {
		cc, err := compileCondition(&r.When[ci], policyIndex, ruleIndex, ci)
		if err != nil {
			return cr, err
		}
		cr.when = append(cr.when, cc)
	}
	return cr, nil
}

func compileSource(s *Source, policyIndex, ruleIndex int) (compiledSource, error) {
	var cs compiledSource
	var err error
	if cs.identities, err = compilePatternSet(s.Identities); err != nil {
		return cs, invalidPolicySetf(policyIndex, "rule %d source identities: %v", ruleIndex, err)
	}
	if cs.notIdentities, err = compilePatternSet(s.NotIdentities); err != nil {
		return cs, invalidPolicySetf(policyIndex, "rule %d source not-identities: %v", ruleIndex, err)
	}
	if cs.namespaces, err = compilePatternSet(s.Namespaces); err != nil {
		return cs, invalidPolicySetf(policyIndex, "rule %d source namespaces: %v", ruleIndex, err)
	}
	if cs.notNamespaces, err = compilePatternSet(s.NotNamespaces); err != nil {
		return cs, invalidPolicySetf(policyIndex, "rule %d source not-namespaces: %v", ruleIndex, err)
	}
	return cs, nil
}

func compileOperation(o *Operation, policyIndex, ruleIndex int) (compiledOperation, error) {
	var co compiledOperation
	var err error
	if co.paths, err = compilePatternSet(o.Paths); err != nil {
		return co, invalidPolicySetf(policyIndex, "rule %d operation paths: %v", ruleIndex, err)
	}
	if co.notPaths, err = compilePatternSet(o.NotPaths); err != nil {
		return co, invalidPolicySetf(policyIndex, "rule %d operation not-paths: %v", ruleIndex, err)
	}
	if co.methods, err = compileExactSet(o.Methods); err != nil {
		return co, invalidPolicySetf(policyIndex, "rule %d operation methods: %v", ruleIndex, err)
	}
	if co.notMethods, err = compileExactSet(o.NotMethods); err != nil {
		return co, invalidPolicySetf(policyIndex, "rule %d operation not-methods: %v", ruleIndex, err)
	}
	if co.ports, err = compilePortSet(o.Ports); err != nil {
		return co, invalidPolicySetf(policyIndex, "rule %d operation ports: %v", ruleIndex, err)
	}
	if co.notPorts, err = compilePortSet(o.NotPorts); err != nil {
		return co, invalidPolicySetf(policyIndex, "rule %d operation not-ports: %v", ruleIndex, err)
	}
	return co, nil
}

func compileCondition(c *Condition, policyIndex, ruleIndex, condIndex int) (compiledCondition, error) {
	var cc compiledCondition
	if c.Key == "" {
		return cc, invalidPolicySetf(policyIndex, "rule %d condition %d: header name must not be empty", ruleIndex, condIndex)
	}
	cc.key = strings.ToLower(c.Key)
	var err error
	if cc.values, err = compileExactSet(c.Values); err != nil {
		return cc, invalidPolicySetf(policyIndex, "rule %d condition %d values: %v", ruleIndex, condIndex, err)
	}
	if cc.notValues, err = compileExactSet(c.NotValues); err != nil {
		return cc, invalidPolicySetf(policyIndex, "rule %d condition %d not-values: %v", ruleIndex, condIndex, err)
	}
	return cc, nil
}

func compilePatternSet(elements []string) ([]pattern, error) {
	if len(elements) == 0 {
		return nil, nil
	}
	out := make([]pattern, len(elements))
	for i, e := range elements {
		p, err := compilePattern(e)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

func compileExactSet(elements []string) ([]string, error) {
	for _, e := range elements {
		if e == "" {
			return nil, errEmptyElement
		}
	}
	return elements, nil
}

func compilePortSet(ports []int) ([]int, error) {
	for _, p := range ports {
		if p < minPort || p > maxPort {
			return nil, errPortRange
		}
	}
	return ports, nil
}

var errPortRange = errors.New("port out of range [1, 65535]")
