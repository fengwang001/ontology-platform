package meshauthz

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file holds an independent, deliberately naive reference model
// of the semantics and a randomized differential test that compares
// the real evaluator against it step by step. The naive model works
// directly on the submitted []Policy with plain loops — no compiled
// forms, no namespace index — so an implementation bug in the fast
// path cannot hide in the model.

// naiveMatchElem mirrors the four element forms.
func naiveMatchElem(elem, value string) bool {
	switch {
	case elem == "*":
		return true
	case strings.HasPrefix(elem, "*"):
		return strings.HasSuffix(value, elem[1:])
	case strings.HasSuffix(elem, "*"):
		return strings.HasPrefix(value, elem[:len(elem)-1])
	default:
		return elem == value
	}
}

func naiveMatchStringSet(value string, pos, neg []string) bool {
	for _, n := range neg {
		if naiveMatchElem(n, value) {
			return false
		}
	}
	if len(pos) == 0 {
		return true
	}
	for _, p := range pos {
		if naiveMatchElem(p, value) {
			return true
		}
	}
	return false
}

func naiveMatchExactSet(value string, pos, neg []string) bool {
	for _, n := range neg {
		if n == value {
			return false
		}
	}
	if len(pos) == 0 {
		return true
	}
	for _, p := range pos {
		if p == value {
			return true
		}
	}
	return false
}

func naiveMatchIntSet(value int, pos, neg []int) bool {
	for _, n := range neg {
		if n == value {
			return false
		}
	}
	if len(pos) == 0 {
		return true
	}
	for _, p := range pos {
		if p == value {
			return true
		}
	}
	return false
}

func naiveHeaderValues(req *Request, key string) ([]string, bool) {
	var out []string
	found := false
	for name, values := range req.Headers {
		if strings.EqualFold(name, key) {
			out = append(out, values...)
			found = true
		}
	}
	return out, found
}

func naiveRuleMatch(r *Rule, req *Request) bool {
	if r.From != nil {
		if !naiveMatchStringSet(req.SourceIdentity, r.From.Identities, r.From.NotIdentities) {
			return false
		}
		if !naiveMatchStringSet(req.SourceNamespace, r.From.Namespaces, r.From.NotNamespaces) {
			return false
		}
	}
	if r.Operation != nil {
		o := r.Operation
		if !naiveMatchExactSet(req.Method, o.Methods, o.NotMethods) {
			return false
		}
		if !naiveMatchStringSet(req.Path, o.Paths, o.NotPaths) {
			return false
		}
		if !naiveMatchIntSet(req.Port, o.Ports, o.NotPorts) {
			return false
		}
	}
	for _, c := range r.When {
		values, present := naiveHeaderValues(req, c.Key)
		if present {
			blocked := false
			for _, v := range values {
				for _, n := range c.NotValues {
					if v == n {
						blocked = true
					}
				}
			}
			if blocked {
				return false
			}
		}
		if len(c.Values) > 0 {
			if !present {
				return false
			}
			ok := false
			for _, v := range values {
				for _, want := range c.Values {
					if v == want {
						ok = true
					}
				}
			}
			if !ok {
				return false
			}
		}
	}
	return true
}

func naiveApplicable(p *Policy, rootNS string, req *Request) bool {
	if p.Namespace != req.TargetNamespace && p.Namespace != rootNS {
		return false
	}
	for k, v := range p.Selector {
		if req.TargetLabels[k] != v {
			return false
		}
	}
	return true
}

func naivePolicyMatch(p *Policy, req *Request) bool {
	for i := range p.Rules {
		if naiveRuleMatch(&p.Rules[i], req) {
			return true
		}
	}
	return false
}

func naiveSortRefs(refs []PolicyRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Namespace < refs[j].Namespace
	})
}

// naiveEvaluate is the reference decision procedure.
func naiveEvaluate(policies []Policy, version uint64, rootNS string, req *Request) Result {
	var denyHits, allowHits, auditHits []PolicyRef
	allowExists := false
	for i := range policies {
		p := &policies[i]
		if !naiveApplicable(p, rootNS, req) {
			continue
		}
		switch p.Action {
		case ActionDeny:
			if naivePolicyMatch(p, req) {
				denyHits = append(denyHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		case ActionAllow:
			allowExists = true
			if naivePolicyMatch(p, req) {
				allowHits = append(allowHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		case ActionAudit:
			if naivePolicyMatch(p, req) {
				auditHits = append(auditHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		}
	}
	naiveSortRefs(auditHits)
	res := Result{Evidence: Evidence{Version: version, AuditPolicies: auditHits}}
	switch {
	case len(denyHits) > 0:
		res.Decision = DecisionDeny
		naiveSortRefs(denyHits)
		res.Evidence.DecisionPolicies = denyHits
	case !allowExists:
		res.Decision = DecisionAllow
	case len(allowHits) > 0:
		res.Decision = DecisionAllow
		naiveSortRefs(allowHits)
		res.Evidence.DecisionPolicies = allowHits
	default:
		res.Decision = DecisionDeny
	}
	return res
}

// --- random generation -------------------------------------------------

var (
	randNamespaces = []string{"prod", "staging", "dev", testRootNS}
	randIdentities = []string{
		"spiffe://cluster.local/ns/prod/sa/web",
		"spiffe://cluster.local/ns/prod/sa/batch",
		"spiffe://cluster.local/ns/staging/sa/web",
		"",
	}
	randMethods = []string{"GET", "POST", "DELETE", "get"}
	randPaths   = []string{"/api/v1/items", "/api/v2/users", "/healthz", "/admin/panel"}
	randLabels  = [][2]string{{"app", "api"}, {"app", "web"}, {"version", "v1"}, {"version", "v2"}}
	randHeaders = [][2]string{{"x-tenant", "blue"}, {"x-tenant", "green"}, {"x-debug", "1"}}
)

// randPatternElem derives a random legal element from a base string:
// exact, prefix, suffix or match-all.
func randPatternElem(rng *rand.Rand, base string) string {
	if base == "" {
		return "*"
	}
	switch rng.Intn(5) {
	case 0:
		return base
	case 1:
		return base[:1+rng.Intn(len(base))] + "*"
	case 2:
		return "*" + base[rng.Intn(len(base)):]
	case 3:
		return "*"
	default:
		return base
	}
}

func randStringSet(rng *rand.Rand, bases []string, patterned bool) []string {
	n := rng.Intn(3)
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		base := bases[rng.Intn(len(bases))]
		if patterned {
			out = append(out, randPatternElem(rng, base))
		} else if base != "" {
			out = append(out, base)
		}
	}
	return out
}

func randPortSet(rng *rand.Rand) []int {
	n := rng.Intn(3)
	if n == 0 {
		return nil
	}
	ports := []int{80, 443, 8080, 9090}
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ports[rng.Intn(len(ports))])
	}
	return out
}

func randRule(rng *rand.Rand) Rule {
	var r Rule
	if rng.Intn(2) == 0 {
		r.From = &Source{
			Identities:    randStringSet(rng, randIdentities, true),
			NotIdentities: randStringSet(rng, randIdentities, true),
			Namespaces:    randStringSet(rng, randNamespaces, true),
			NotNamespaces: randStringSet(rng, randNamespaces, true),
		}
	}
	if rng.Intn(2) == 0 {
		r.Operation = &Operation{
			Methods:    randStringSet(rng, randMethods, false),
			NotMethods: randStringSet(rng, randMethods, false),
			Paths:      randStringSet(rng, randPaths, true),
			NotPaths:   randStringSet(rng, randPaths, true),
			Ports:      randPortSet(rng),
			NotPorts:   randPortSet(rng),
		}
	}
	if rng.Intn(2) == 0 {
		n := 1 + rng.Intn(2)
		for i := 0; i < n; i++ {
			h := randHeaders[rng.Intn(len(randHeaders))]
			r.When = append(r.When, Condition{
				Key:       h[0],
				Values:    randStringSet(rng, []string{h[1], "red"}, false),
				NotValues: randStringSet(rng, []string{h[1], "red"}, false),
			})
		}
	}
	return r
}

// randPolicySet generates a random valid policy set: names are unique
// per namespace by construction.
func randPolicySet(rng *rand.Rand) []Policy {
	n := rng.Intn(9)
	policies := make([]Policy, 0, n)
	for i := 0; i < n; i++ {
		ns := randNamespaces[rng.Intn(len(randNamespaces))]
		var selector map[string]string
		if rng.Intn(2) == 0 {
			selector = map[string]string{}
			for _, kv := range randLabels {
				if rng.Intn(3) == 0 {
					selector[kv[0]] = kv[1]
				}
			}
		}
		var rules []Rule
		if rng.Intn(4) != 0 { // 1 in 4 policies gets an empty rule list
			for j := 0; j < rng.Intn(3); j++ {
				rules = append(rules, randRule(rng))
			}
		}
		actions := []Action{ActionAllow, ActionDeny, ActionAudit}
		policies = append(policies, Policy{
			Name:      fmt.Sprintf("p%d", i),
			Namespace: ns,
			Selector:  selector,
			Action:    actions[rng.Intn(len(actions))],
			Rules:     rules,
		})
	}
	return policies
}

func randRequest(rng *rand.Rand) *Request {
	labels := map[string]string{}
	for _, kv := range randLabels {
		if rng.Intn(2) == 0 {
			labels[kv[0]] = kv[1]
		}
	}
	headers := map[string][]string{}
	for _, kv := range randHeaders {
		if rng.Intn(2) == 0 {
			key := kv[0]
			if rng.Intn(2) == 0 {
				key = strings.ToUpper(key) // exercise case-insensitivity
			}
			headers[key] = append(headers[key], kv[1])
			if rng.Intn(3) == 0 {
				headers[key] = append(headers[key], "extra")
			}
		}
	}
	ports := []int{80, 443, 8080, 9090}
	return &Request{
		SourceIdentity:  randIdentities[rng.Intn(len(randIdentities))],
		SourceNamespace: randNamespaces[rng.Intn(len(randNamespaces))],
		TargetNamespace: randNamespaces[rng.Intn(len(randNamespaces))],
		TargetLabels:    labels,
		Method:          randMethods[rng.Intn(len(randMethods))],
		Path:            randPaths[rng.Intn(len(randPaths))],
		Port:            ports[rng.Intn(len(ports))],
		Headers:         headers,
	}
}

// TestDifferentialAgainstNaiveModel replaces the whole policy set with
// random valid sets and compares every evaluation against the naive
// reference model, logging each operation's input, actual output and
// the model's verdict.
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	seed := int64(20261007)
	rng := rand.New(rand.NewSource(seed))
	t.Logf("differential test seed=%d", seed)

	s := newTestStore(t)
	var installed []Policy
	var version uint64

	const rounds = 300
	for round := 0; round < rounds; round++ {
		candidate := randPolicySet(rng)
		v, err := s.ReplaceAll(candidate)
		if err != nil {
			t.Fatalf("round %d: generated set must be valid: %v", round, err)
		}
		installed = candidate
		version = v
		t.Logf("round %d: replace version=%d policies=%d", round, version, len(candidate))

		for i := 0; i < 10; i++ {
			req := randRequest(rng)
			got, err := s.Evaluate(req)
			if err != nil {
				t.Fatalf("round %d eval %d: %v", round, i, err)
			}
			want := naiveEvaluate(installed, version, testRootNS, req)
			match := reflect.DeepEqual(normalizeResult(got), normalizeResult(want))
			t.Logf("round %d eval %d: req=%+v actual=%s want=%s match=%v evidence=%+v",
				round, i, req, got.Decision, want.Decision, match, got.Evidence)
			if !match {
				t.Fatalf("round %d eval %d mismatch:\n req=%+v\n got=%+v\n want=%+v",
					round, i, req, got, want)
			}
		}
	}
}

// normalizeResult nil-normalizes empty slices so DeepEqual compares
// semantics, not allocation artifacts.
func normalizeResult(r Result) Result {
	if len(r.Evidence.DecisionPolicies) == 0 {
		r.Evidence.DecisionPolicies = nil
	}
	if len(r.Evidence.AuditPolicies) == 0 {
		r.Evidence.AuditPolicies = nil
	}
	return r
}
