package meshauthz

import "strings"

// normalizedRequest is the evaluation-time view of a request: header
// names are lower-cased once so condition lookup is case-insensitive
// without per-condition normalization.
type normalizedRequest struct {
	req     *Request
	headers map[string][]string
}

func normalizeRequest(req *Request) normalizedRequest {
	headers := make(map[string][]string, len(req.Headers))
	for name, values := range req.Headers {
		key := strings.ToLower(name)
		headers[key] = append(headers[key], values...)
	}
	return normalizedRequest{req: req, headers: headers}
}

// selectorMatches reports whether the target workload's labels satisfy
// the selector: every listed key/value must be equal. An empty
// selector matches every workload in the namespace.
func selectorMatches(selector map[string]string, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// sourceMatches evaluates the "from" part: identity and namespace are
// ANDed, each following the shared set semantics.
func sourceMatches(s *compiledSource, req *Request) bool {
	return matchStringSet(req.SourceIdentity, s.identities, s.notIdentities) &&
		matchStringSet(req.SourceNamespace, s.namespaces, s.notNamespaces)
}

// operationMatches evaluates the "to" part: method (exact,
// case-sensitive), path (pattern forms) and port (numeric) are ANDed.
func operationMatches(o *compiledOperation, req *Request) bool {
	return matchExactSet(req.Method, o.methods, o.notMethods) &&
		matchStringSet(req.Path, o.paths, o.notPaths) &&
		matchIntSet(req.Port, o.ports, o.notPorts)
}

// conditionMatches evaluates one header condition. A missing header
// fails the condition when Values is non-empty and satisfies it when
// only NotValues is set (no value can be in the negative set).
func conditionMatches(c *compiledCondition, headers map[string][]string) bool {
	values, present := headers[c.key]
	if present {
		for _, v := range values {
			for _, n := range c.notValues {
				if v == n {
					return false
				}
			}
		}
	}
	if len(c.values) == 0 {
		return true
	}
	if !present {
		return false
	}
	for _, v := range values {
		for _, want := range c.values {
			if v == want {
				return true
			}
		}
	}
	return false
}

// ruleMatches evaluates the conjunction of the three parts; an absent
// part imposes no constraint, so a fully empty rule matches everything.
func ruleMatches(r *compiledRule, req *normalizedRequest) bool {
	if r.from != nil && !sourceMatches(r.from, req.req) {
		return false
	}
	if r.operation != nil && !operationMatches(r.operation, req.req) {
		return false
	}
	for i := range r.when {
		if !conditionMatches(&r.when[i], req.headers) {
			return false
		}
	}
	return true
}

// policyMatches reports whether at least one rule is satisfied. A
// policy with an empty rule list never matches.
func policyMatches(p *compiledPolicy, req *normalizedRequest) bool {
	for i := range p.rules {
		if ruleMatches(&p.rules[i], req) {
			return true
		}
	}
	return false
}
