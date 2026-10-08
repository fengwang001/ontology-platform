package authz

import (
	"sort"
	"strings"
)

// evaluate runs the decision procedure against one immutable snapshot and
// also reports how many candidate policies were examined, so tests can
// prove the cost is independent of unrelated namespaces.
func evaluate(snap *snapshot, rootNamespace string, req *Request) (Result, int) {
	headers := make(map[string][]string, len(req.Headers))
	for name, values := range req.Headers {
		headers[strings.ToLower(name)] = values
	}

	candidates := snap.byNamespace[req.TargetNamespace]
	examined := len(candidates)
	var rootPolicies []*Policy
	if rootNamespace != req.TargetNamespace {
		rootPolicies = snap.byNamespace[rootNamespace]
		examined += len(rootPolicies)
	}

	var denyHits, allowHits, auditHits []PolicyRef
	allowApplicable := false
	visit := func(p *Policy) {
		if !applicable(p, req, rootNamespace) {
			return
		}
		switch p.Action {
		case ActionDeny:
			if policyHit(p, req, headers) {
				denyHits = append(denyHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		case ActionAllow:
			allowApplicable = true
			if policyHit(p, req, headers) {
				allowHits = append(allowHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		case ActionAudit:
			if policyHit(p, req, headers) {
				auditHits = append(auditHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		}
	}
	for _, p := range candidates {
		visit(p)
	}
	for _, p := range rootPolicies {
		visit(p)
	}

	res := Result{Version: snap.version, AuditPolicies: sortRefs(auditHits)}
	switch {
	case len(denyHits) > 0:
		res.Decision = DecisionDeny
		res.DecisionPolicies = sortRefs(denyHits)
	case !allowApplicable:
		res.Decision = DecisionAllow
	case len(allowHits) > 0:
		res.Decision = DecisionAllow
		res.DecisionPolicies = sortRefs(allowHits)
	default:
		res.Decision = DecisionDeny
	}
	return res, examined
}

func sortRefs(refs []PolicyRef) []PolicyRef {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Namespace < refs[j].Namespace
	})
	return refs
}
