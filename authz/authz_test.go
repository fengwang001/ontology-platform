package authz

import (
	"testing"
)

const testRoot = "istio-system"

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(testRoot)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func mustReplace(t *testing.T, s *Store, policies ...Policy) uint64 {
	t.Helper()
	v, err := s.ReplaceAll(policies)
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	return v
}

func mustEval(t *testing.T, s *Store, req Request) Result {
	t.Helper()
	res, err := s.Evaluate(req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return res
}

func baseReq() Request {
	return Request{
		SourceIdentity:  "spiffe://cluster.local/ns/default/sa/web",
		SourceNamespace: "default",
		TargetNamespace: "prod",
		TargetLabels:    map[string]string{"app": "api", "tier": "backend"},
		Method:          "GET",
		Path:            "/v1/items",
		Port:            8080,
	}
}

func allowPolicy(name, ns string, rules ...Rule) Policy {
	return Policy{Name: name, Namespace: ns, Action: ActionAllow, Rules: rules}
}

func denyPolicy(name, ns string, rules ...Rule) Policy {
	return Policy{Name: name, Namespace: ns, Action: ActionDeny, Rules: rules}
}

func auditPolicy(name, ns string, rules ...Rule) Policy {
	return Policy{Name: name, Namespace: ns, Action: ActionAudit, Rules: rules}
}

func refNames(refs []PolicyRef) []string {
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Namespace+"/"+r.Name)
	}
	return names
}

func equalRefs(got []PolicyRef, want ...PolicyRef) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- Applicability: namespace, root namespace, selector -------------------

func TestApplicabilityNamespace(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s, allowPolicy("p", "other", Rule{}))
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionAllow || len(res.DecisionPolicies) != 0 {
		t.Fatalf("policy in unrelated namespace must not apply; got %+v", res)
	}
	mustReplace(t, s, allowPolicy("p", "prod", Rule{}))
	res = mustEval(t, s, baseReq())
	if res.Decision != DecisionAllow || !equalRefs(res.DecisionPolicies, PolicyRef{"p", "prod", ActionAllow}) {
		t.Fatalf("policy in target namespace must apply; got %+v", res)
	}
}

func TestApplicabilityRootNamespace(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s, allowPolicy("global", testRoot, Rule{}))
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionAllow || !equalRefs(res.DecisionPolicies, PolicyRef{"global", testRoot, ActionAllow}) {
		t.Fatalf("root-namespace policy must apply mesh-wide; got %+v", res)
	}
	// Root policy selector still has to be satisfied.
	p := allowPolicy("global", testRoot, Rule{})
	p.Selector = map[string]string{"app": "not-api"}
	mustReplace(t, s, p)
	res = mustEval(t, s, baseReq())
	if res.Decision != DecisionAllow || len(res.DecisionPolicies) != 0 {
		t.Fatalf("root policy with unsatisfied selector must not hit; got %+v", res)
	}
}

func TestApplicabilitySelector(t *testing.T) {
	s := newStore(t)
	p := allowPolicy("sel", "prod", Rule{})
	p.Selector = map[string]string{"app": "api", "tier": "backend"}
	mustReplace(t, s, p)
	if res := mustEval(t, s, baseReq()); len(res.DecisionPolicies) != 1 {
		t.Fatalf("fully satisfied selector must apply; got %+v", res)
	}
	p.Selector = map[string]string{"app": "api", "tier": "frontend"}
	mustReplace(t, s, p)
	if res := mustEval(t, s, baseReq()); len(res.DecisionPolicies) != 0 {
		t.Fatalf("partially satisfied selector must not apply; got %+v", res)
	}
	// Empty selector selects all workloads in scope.
	p.Selector = nil
	mustReplace(t, s, p)
	if res := mustEval(t, s, baseReq()); len(res.DecisionPolicies) != 1 {
		t.Fatalf("empty selector must select all; got %+v", res)
	}
}

// --- Decision order --------------------------------------------------------

func TestDenyOverridesAllow(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s,
		allowPolicy("allow-all", "prod", Rule{}),
		denyPolicy("deny-get", "prod", Rule{Operation: &OperationSpec{Methods: []string{"GET"}}}),
	)
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionDeny {
		t.Fatalf("deny must override allow; got %+v", res)
	}
	if !equalRefs(res.DecisionPolicies, PolicyRef{"deny-get", "prod", ActionDeny}) {
		t.Fatalf("evidence must list the hitting deny policy; got %+v", res.DecisionPolicies)
	}
}

func TestDefaultAllowWithoutAllowPolicies(t *testing.T) {
	s := newStore(t)
	// Only deny and audit policies exist, none matching: default allow.
	mustReplace(t, s,
		denyPolicy("deny-post", "prod", Rule{Operation: &OperationSpec{Methods: []string{"POST"}}}),
		auditPolicy("audit-none", "prod", Rule{Operation: &OperationSpec{Methods: []string{"DELETE"}}}),
	)
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionAllow || len(res.DecisionPolicies) != 0 {
		t.Fatalf("expected default allow with empty decision evidence; got %+v", res)
	}
	// Even a hitting deny still decides, but with no allow policies and no
	// deny hit the request passes.
	mustReplace(t, s, auditPolicy("a", "prod", Rule{}))
	res = mustEval(t, s, baseReq())
	if res.Decision != DecisionAllow || len(res.DecisionPolicies) != 0 {
		t.Fatalf("audit-only set must default-allow; got %+v", res)
	}
	if !equalRefs(res.AuditPolicies, PolicyRef{"a", "prod", ActionAudit}) {
		t.Fatalf("audit evidence missing; got %+v", res.AuditPolicies)
	}
}

func TestAllowPolicyWithEmptyRuleListDeniesEverything(t *testing.T) {
	s := newStore(t)
	// An allow policy with zero rules matches nothing, but its existence
	// switches the mesh to default-deny for applicable targets.
	mustReplace(t, s, allowPolicy("empty-rules", "prod"))
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionDeny || len(res.DecisionPolicies) != 0 {
		t.Fatalf("expected deny with empty decision evidence; got %+v", res)
	}
}

func TestEmptyRuleMatchesEverything(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s, allowPolicy("all", "prod", Rule{}))
	req := baseReq()
	req.Method = "WEIRD"
	req.Path = "/anything/at/all"
	req.Port = 1
	res := mustEval(t, s, req)
	if res.Decision != DecisionAllow || len(res.DecisionPolicies) != 1 {
		t.Fatalf("empty rule must match any request; got %+v", res)
	}
}

// --- Element forms ---------------------------------------------------------

func TestElementForms(t *testing.T) {
	cases := []struct {
		element, value string
		want           bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"exact", "exact", true},
		{"exact", "Exact", false},
		{"exact", "exact2", false},
		{"/v1/*", "/v1/items", true},
		{"/v1/*", "/v1", false},
		{"/v1/*", "/v2/items", false},
		{"*/items", "/v1/items", true},
		{"*/items", "items", false},
		{"*/items", "/v1/other", false},
		{"cluster.local/ns/default/*", "cluster.local/ns/default/sa/web", true},
		{"*/sa/web", "cluster.local/ns/default/sa/web", true},
	}
	for _, c := range cases {
		if got := matchElement(c.element, c.value); got != c.want {
			t.Errorf("matchElement(%q, %q) = %v, want %v", c.element, c.value, got, c.want)
		}
	}
}

func TestPatternFormsInRules(t *testing.T) {
	s := newStore(t)
	rule := Rule{Source: &SourceSpec{
		Identities: []string{"*/sa/web"},
		Namespaces: []string{"def*"},
	}, Operation: &OperationSpec{Paths: []string{"/v1/*"}}}
	mustReplace(t, s, allowPolicy("patterns", "prod", rule))
	if res := mustEval(t, s, baseReq()); res.Decision != DecisionAllow || len(res.DecisionPolicies) != 1 {
		t.Fatalf("prefix/suffix patterns must match; got %+v", res)
	}
	// Star-alone namespace pattern matches any source namespace.
	rule.Source.Namespaces = []string{"*"}
	mustReplace(t, s, allowPolicy("patterns", "prod", rule))
	req := baseReq()
	req.SourceNamespace = "anything"
	if res := mustEval(t, s, req); res.Decision != DecisionAllow || len(res.DecisionPolicies) != 1 {
		t.Fatalf("lone star must match all; got %+v", res)
	}
}

func TestMethodCaseSensitive(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s, allowPolicy("m", "prod", Rule{Operation: &OperationSpec{Methods: []string{"GET"}}}))
	req := baseReq()
	req.Method = "get"
	res := mustEval(t, s, req)
	if res.Decision != DecisionDeny {
		t.Fatalf("method comparison must be case-sensitive; got %+v", res)
	}
}

// --- Negation and header conditions ---------------------------------------

func TestNegationVetoes(t *testing.T) {
	s := newStore(t)
	rule := Rule{
		Source:    &SourceSpec{Identities: []string{"*"}, NotIdentities: []string{"*/sa/blocked"}},
		Operation: &OperationSpec{Ports: []int{1, 8080}, NotPorts: []int{9090}},
	}
	mustReplace(t, s, allowPolicy("neg", "prod", rule))
	if res := mustEval(t, s, baseReq()); res.Decision != DecisionAllow {
		t.Fatalf("expected allow; got %+v", res)
	}
	req := baseReq()
	req.SourceIdentity = "cluster.local/ns/default/sa/blocked"
	if res := mustEval(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negated identity must veto the rule; got %+v", res)
	}
	req = baseReq()
	req.Port = 9090
	if res := mustEval(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negated port must veto the rule; got %+v", res)
	}
}

func TestHeaderConditions(t *testing.T) {
	s := newStore(t)
	rule := Rule{Conditions: []Condition{
		{Header: "X-Tenant", Values: []string{"a", "b"}},
		{Header: "X-Env", NotValues: []string{"debug"}},
	}}
	mustReplace(t, s, allowPolicy("cond", "prod", rule))

	req := baseReq()
	req.Headers = map[string][]string{"x-tenant": {"b"}, "X-ENV": {"prod"}}
	if res := mustEval(t, s, req); res.Decision != DecisionAllow {
		t.Fatalf("conditions satisfied (case-insensitive names); got %+v", res)
	}
	// Missing required-value header fails the condition.
	req.Headers = map[string][]string{"X-Env": {"prod"}}
	if res := mustEval(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("missing required header must fail; got %+v", res)
	}
	// Missing negation-only header satisfies the condition.
	req.Headers = map[string][]string{"X-Tenant": {"a"}}
	if res := mustEval(t, s, req); res.Decision != DecisionAllow {
		t.Fatalf("missing negation-only header must satisfy; got %+v", res)
	}
	// A negated value vetoes.
	req.Headers = map[string][]string{"X-Tenant": {"a"}, "X-Env": {"debug"}}
	if res := mustEval(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negated header value must veto; got %+v", res)
	}
	// Multi-value header: any value satisfies the positive set, any value
	// triggers the negation.
	req.Headers = map[string][]string{"X-Tenant": {"z", "b"}}
	if res := mustEval(t, s, req); res.Decision != DecisionAllow {
		t.Fatalf("any matching value must satisfy; got %+v", res)
	}
	req.Headers = map[string][]string{"X-Tenant": {"a"}, "X-Env": {"prod", "debug"}}
	if res := mustEval(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("any negated value must veto; got %+v", res)
	}
}

// --- Audit and evidence ----------------------------------------------------

func TestAuditDoesNotChangeDecision(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s,
		auditPolicy("audit-hit", "prod", Rule{}),
		allowPolicy("allow-get", "prod", Rule{Operation: &OperationSpec{Methods: []string{"POST"}}}),
	)
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionDeny {
		t.Fatalf("audit hit must not allow the request; got %+v", res)
	}
	if len(res.DecisionPolicies) != 0 {
		t.Fatalf("no-allow-hit deny must have empty decision evidence; got %+v", res.DecisionPolicies)
	}
	if !equalRefs(res.AuditPolicies, PolicyRef{"audit-hit", "prod", ActionAudit}) {
		t.Fatalf("audit evidence must list the hit; got %+v", res.AuditPolicies)
	}
}

func TestAuditEvidenceSortedAndScoped(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s,
		auditPolicy("b", "prod", Rule{}),
		auditPolicy("a", "prod", Rule{}),
		auditPolicy("a", testRoot, Rule{}),
		auditPolicy("miss", "prod", Rule{Operation: &OperationSpec{Methods: []string{"POST"}}}),
		auditPolicy("other-ns", "other", Rule{}),
		allowPolicy("allow", "prod", Rule{}),
	)
	res := mustEval(t, s, baseReq())
	want := []PolicyRef{
		{"a", testRoot, ActionAudit},
		{"a", "prod", ActionAudit},
		{"b", "prod", ActionAudit},
	}
	if !equalRefs(res.AuditPolicies, want...) {
		t.Fatalf("audit evidence must be sorted by (name, namespace) and scoped; got %v want %v",
			refNames(res.AuditPolicies), refNames(want))
	}
	if !equalRefs(res.DecisionPolicies, PolicyRef{"allow", "prod", ActionAllow}) {
		t.Fatalf("decision evidence wrong; got %+v", res.DecisionPolicies)
	}
}

func TestAllHittingDenyPoliciesListed(t *testing.T) {
	s := newStore(t)
	mustReplace(t, s,
		denyPolicy("d2", "prod", Rule{}),
		denyPolicy("d1", "prod", Rule{}),
		denyPolicy("d3", testRoot, Rule{}),
		denyPolicy("d4", "prod", Rule{Operation: &OperationSpec{Methods: []string{"POST"}}}),
		allowPolicy("allow", "prod", Rule{}),
	)
	res := mustEval(t, s, baseReq())
	if res.Decision != DecisionDeny {
		t.Fatalf("expected deny; got %+v", res)
	}
	want := []PolicyRef{
		{"d1", "prod", ActionDeny},
		{"d2", "prod", ActionDeny},
		{"d3", testRoot, ActionDeny},
	}
	if !equalRefs(res.DecisionPolicies, want...) {
		t.Fatalf("all hitting deny policies must be listed, sorted; got %v want %v",
			refNames(res.DecisionPolicies), refNames(want))
	}
}

func TestVersionInEvidence(t *testing.T) {
	s := newStore(t)
	if res := mustEval(t, s, baseReq()); res.Version != 0 {
		t.Fatalf("empty store must evaluate at version 0; got %d", res.Version)
	}
	v := mustReplace(t, s, allowPolicy("a", "prod", Rule{}))
	res := mustEval(t, s, baseReq())
	if res.Version != v || v != 1 {
		t.Fatalf("result must carry the observed version; got res=%d v=%d", res.Version, v)
	}
}
