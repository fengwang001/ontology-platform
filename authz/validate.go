package authz

import (
	"fmt"
	"strings"
)

// MinPort and MaxPort bound legal port numbers.
const (
	MinPort = 1
	MaxPort = 65535
)

// validateRequest enforces request-field legality (ErrKindInvalidArgument).
func validateRequest(req *Request) error {
	if req.SourceNamespace == "" {
		return argErr("SourceNamespace", "must not be empty")
	}
	if req.TargetNamespace == "" {
		return argErr("TargetNamespace", "must not be empty")
	}
	if req.Method == "" {
		return argErr("Method", "must not be empty")
	}
	if req.Port < MinPort || req.Port > MaxPort {
		return argErr("Port", fmt.Sprintf("must be in [%d, %d], got %d", MinPort, MaxPort, req.Port))
	}
	for name, values := range req.Headers {
		if name == "" {
			return argErr("Headers", "header name must not be empty")
		}
		for i, v := range values {
			if v == "" {
				return argErr("Headers", fmt.Sprintf("header %q value[%d] must not be empty", name, i))
			}
		}
	}
	return nil
}

// validatePatternElement checks the star forms: a '*' may appear only at the
// start or the end, never both, never in the middle; a lone '*' is legal.
func validatePatternElement(e string) error {
	if e == "" {
		return fmt.Errorf("element must not be empty")
	}
	n := strings.Count(e, "*")
	if n == 0 {
		return nil
	}
	if e == "*" {
		return nil
	}
	if n > 1 {
		return fmt.Errorf("element %q: '*' may appear at most once", e)
	}
	if !strings.HasPrefix(e, "*") && !strings.HasSuffix(e, "*") {
		return fmt.Errorf("element %q: '*' may only appear at the start or the end", e)
	}
	return nil
}

func validatePatternSet(idx int, policy, field string, set []string) error {
	for i, e := range set {
		if err := validatePatternElement(e); err != nil {
			return policyErr(idx, policy, fmt.Sprintf("%s[%d]", field, i), err.Error())
		}
	}
	return nil
}

func validateExactSet(idx int, policy, field string, set []string) error {
	for i, e := range set {
		if e == "" {
			return policyErr(idx, policy, fmt.Sprintf("%s[%d]", field, i), "element must not be empty")
		}
	}
	return nil
}

func validatePortSet(idx int, policy, field string, set []int) error {
	for i, p := range set {
		if p < MinPort || p > MaxPort {
			return policyErr(idx, policy, fmt.Sprintf("%s[%d]", field, i),
				fmt.Sprintf("port must be in [%d, %d], got %d", MinPort, MaxPort, p))
		}
	}
	return nil
}

func validateRule(idx int, policy string, ri int, r *Rule) error {
	prefix := fmt.Sprintf("Rules[%d]", ri)
	if s := r.Source; s != nil {
		if err := validatePatternSet(idx, policy, prefix+".Source.Identities", s.Identities); err != nil {
			return err
		}
		if err := validatePatternSet(idx, policy, prefix+".Source.NotIdentities", s.NotIdentities); err != nil {
			return err
		}
		if err := validatePatternSet(idx, policy, prefix+".Source.Namespaces", s.Namespaces); err != nil {
			return err
		}
		if err := validatePatternSet(idx, policy, prefix+".Source.NotNamespaces", s.NotNamespaces); err != nil {
			return err
		}
	}
	if o := r.Operation; o != nil {
		if err := validateExactSet(idx, policy, prefix+".Operation.Methods", o.Methods); err != nil {
			return err
		}
		if err := validateExactSet(idx, policy, prefix+".Operation.NotMethods", o.NotMethods); err != nil {
			return err
		}
		if err := validatePatternSet(idx, policy, prefix+".Operation.Paths", o.Paths); err != nil {
			return err
		}
		if err := validatePatternSet(idx, policy, prefix+".Operation.NotPaths", o.NotPaths); err != nil {
			return err
		}
		if err := validatePortSet(idx, policy, prefix+".Operation.Ports", o.Ports); err != nil {
			return err
		}
		if err := validatePortSet(idx, policy, prefix+".Operation.NotPorts", o.NotPorts); err != nil {
			return err
		}
	}
	for ci, c := range r.Conditions {
		field := fmt.Sprintf("%s.Conditions[%d]", prefix, ci)
		if c.Header == "" {
			return policyErr(idx, policy, field+".Header", "header name must not be empty")
		}
		if err := validateExactSet(idx, policy, field+".Values", c.Values); err != nil {
			return err
		}
		if err := validateExactSet(idx, policy, field+".NotValues", c.NotValues); err != nil {
			return err
		}
	}
	return nil
}

// validatePolicySet checks a whole submitted set and reports the first
// problem found in submission order.
func validatePolicySet(policies []Policy) error {
	type key struct{ namespace, name string }
	seen := make(map[key]struct{}, len(policies))
	for i := range policies {
		p := &policies[i]
		if p.Name == "" {
			return policyErr(i, p.Name, "Name", "must not be empty")
		}
		if p.Namespace == "" {
			return policyErr(i, p.Name, "Namespace", "must not be empty")
		}
		k := key{p.Namespace, p.Name}
		if _, dup := seen[k]; dup {
			return policyErr(i, p.Name, "Name",
				fmt.Sprintf("duplicate policy name %q in namespace %q", p.Name, p.Namespace))
		}
		seen[k] = struct{}{}
		if !ActionValid(p.Action) {
			return policyErr(i, p.Name, "Action", fmt.Sprintf("illegal action %d", int(p.Action)))
		}
		for sk, sv := range p.Selector {
			if sk == "" {
				return policyErr(i, p.Name, "Selector", "selector key must not be empty")
			}
			if sv == "" {
				return policyErr(i, p.Name, "Selector", fmt.Sprintf("selector value for key %q must not be empty", sk))
			}
		}
		for ri := range p.Rules {
			if err := validateRule(i, p.Name, ri, &p.Rules[ri]); err != nil {
				return err
			}
		}
	}
	return nil
}
