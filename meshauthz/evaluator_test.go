package meshauthz

import (
	"reflect"
	"testing"
)

const testRootNS = "istio-system"

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(testRootNS)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// baseRequest is a fully legal request; tests mutate copies of it.
func baseRequest() *Request {
	return &Request{
		SourceIdentity:  "spiffe://cluster.local/ns/prod/sa/web",
		SourceNamespace: "prod",
		TargetNamespace: "prod",
		TargetLabels:    map[string]string{"app": "api", "version": "v1"},
		Method:          "GET",
		Path:            "/api/v1/items",
		Port:            8080,
		Headers:         map[string][]string{"X-Tenant": {"blue"}},
	}
}

// allowAllPolicy matches every request in namespace ns.
func allowAllPolicy(name, ns string) Policy {
	return Policy{
		Name:      name,
		Namespace: ns,
		Action:    ActionAllow,
		Rules:     []Rule{{}},
	}
}

func mustReplace(t *testing.T, s *Store, policies []Policy) uint64 {
	t.Helper()
	v, err := s.ReplaceAll(policies)
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	return v
}

func mustEvaluate(t *testing.T, s *Store, req *Request) Result {
	t.Helper()
	res, err := s.Evaluate(req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	t.Logf("eval decision=%s evidence=%+v", res.Decision, res.Evidence)
	return res
}

// Applicability: a policy applies iff its namespace equals the target
// namespace OR the root namespace, and its selector is satisfied.
func TestApplicabilityNamespaceRootAndSelector(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		allowAllPolicy("same-ns", "prod"),
		{
			Name:      "root-deny",
			Namespace: testRootNS,
			Action:    ActionDeny,
			Rules:     []Rule{{Operation: &Operation{Methods: []string{"DELETE"}}}},
		},
		{
			Name:      "other-ns-deny",
			Namespace: "other",
			Action:    ActionDeny,
			Rules:     []Rule{{}},
		},
		{
			Name:      "selector-miss-deny",
			Namespace: "prod",
			Selector:  map[string]string{"app": "billing"},
			Action:    ActionDeny,
			Rules:     []Rule{{}},
		},
	})

	// Same-namespace allow matches; root deny does not match GET;
	// other-namespace and selector-missing denies are not applicable.
	res := mustEvaluate(t, s, baseRequest())
	if res.Decision != DecisionAllow {
		t.Fatalf("want ALLOW, got %s", res.Decision)
	}

	// The root-namespace deny matches DELETE in any namespace.
	req := baseRequest()
	req.Method = "DELETE"
	req.TargetNamespace = "staging"
	res = mustEvaluate(t, s, req)
	if res.Decision != DecisionDeny {
		t.Fatalf("root policy must apply mesh-wide, want DENY, got %s", res.Decision)
	}
	want := []PolicyRef{{Name: "root-deny", Namespace: testRootNS, Action: ActionDeny}}
	if !reflect.DeepEqual(res.Evidence.DecisionPolicies, want) {
		t.Fatalf("decision policies = %+v, want %+v", res.Evidence.DecisionPolicies, want)
	}

	// The other-namespace deny never applies, even with a catch-all rule.
	req = baseRequest()
	req.TargetNamespace = "other"
	res = mustEvaluate(t, s, req)
	if res.Decision != DecisionDeny {
		t.Fatalf("want DENY (other-ns-deny applies in its own ns), got %s", res.Decision)
	}
}

// A root-namespace policy still requires its selector to be satisfied.
func TestRootPolicySelectorStillRequired(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		{
			Name:      "root-selective-deny",
			Namespace: testRootNS,
			Selector:  map[string]string{"app": "api"},
			Action:    ActionDeny,
			Rules:     []Rule{{}},
		},
	})

	res := mustEvaluate(t, s, baseRequest())
	if res.Decision != DecisionDeny {
		t.Fatalf("selector satisfied, want DENY, got %s", res.Decision)
	}

	req := baseRequest()
	req.TargetLabels = map[string]string{"app": "web"}
	res = mustEvaluate(t, s, req)
	if res.Decision != DecisionAllow {
		t.Fatalf("selector not satisfied, want default ALLOW, got %s", res.Decision)
	}
}

// An empty selector matches every workload in the namespace.
func TestEmptySelectorMatchesAll(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		{
			Name:      "deny-all-in-prod",
			Namespace: "prod",
			Selector:  map[string]string{},
			Action:    ActionDeny,
			Rules:     []Rule{{}},
		},
	})
	req := baseRequest()
	req.TargetLabels = map[string]string{"unrelated": "labels"}
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("want DENY, got %s", res.Decision)
	}
}

// Deny wins over allow when both match.
func TestDenyPrecedence(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		allowAllPolicy("allow-all", "prod"),
		{
			Name:      "deny-delete",
			Namespace: "prod",
			Action:    ActionDeny,
			Rules:     []Rule{{Operation: &Operation{Methods: []string{"DELETE"}}}},
		},
	})
	req := baseRequest()
	req.Method = "DELETE"
	res := mustEvaluate(t, s, req)
	if res.Decision != DecisionDeny {
		t.Fatalf("deny must take precedence, got %s", res.Decision)
	}
	want := []PolicyRef{{Name: "deny-delete", Namespace: "prod", Action: ActionDeny}}
	if !reflect.DeepEqual(res.Evidence.DecisionPolicies, want) {
		t.Fatalf("decision policies = %+v, want %+v", res.Evidence.DecisionPolicies, want)
	}
}

// With no applicable allow policy at all, the default is allow and the
// decision part of the evidence is empty.
func TestDefaultAllowWithoutAllowPolicies(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		{
			Name:      "deny-post",
			Namespace: "prod",
			Action:    ActionDeny,
			Rules:     []Rule{{Operation: &Operation{Methods: []string{"POST"}}}},
		},
		{
			Name:      "audit-all",
			Namespace: "prod",
			Action:    ActionAudit,
			Rules:     []Rule{{}},
		},
	})
	res := mustEvaluate(t, s, baseRequest())
	if res.Decision != DecisionAllow {
		t.Fatalf("want default ALLOW, got %s", res.Decision)
	}
	if len(res.Evidence.DecisionPolicies) != 0 {
		t.Fatalf("default allow must carry empty decision policies, got %+v", res.Evidence.DecisionPolicies)
	}
	if len(res.Evidence.AuditPolicies) != 1 || res.Evidence.AuditPolicies[0].Name != "audit-all" {
		t.Fatalf("audit evidence = %+v", res.Evidence.AuditPolicies)
	}
}

// An allow policy with an empty rule list matches nothing, so once any
// applicable allow policy exists, every request is denied.
func TestEmptyRuleListAllowDeniesEverything(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		{Name: "allow-nothing", Namespace: "prod", Action: ActionAllow},
	})
	res := mustEvaluate(t, s, baseRequest())
	if res.Decision != DecisionDeny {
		t.Fatalf("allow policy with empty rules must not match; want DENY, got %s", res.Decision)
	}
	if len(res.Evidence.DecisionPolicies) != 0 {
		t.Fatalf("no-allow-match deny must carry empty decision policies, got %+v", res.Evidence.DecisionPolicies)
	}
}

// A fully empty rule matches every request.
func TestEmptyRuleMatchesAll(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{allowAllPolicy("allow-all", "prod")})
	req := baseRequest()
	req.Method = "PATCH"
	req.Path = "/anything/at/all"
	req.Port = 1
	req.Headers = nil
	res := mustEvaluate(t, s, req)
	if res.Decision != DecisionAllow {
		t.Fatalf("empty rule must match everything, got %s", res.Decision)
	}
	want := []PolicyRef{{Name: "allow-all", Namespace: "prod", Action: ActionAllow}}
	if !reflect.DeepEqual(res.Evidence.DecisionPolicies, want) {
		t.Fatalf("decision policies = %+v, want %+v", res.Evidence.DecisionPolicies, want)
	}
}

// Each pattern-bearing field (identity, namespace, path) supports
// exact, prefix ("a*"), suffix ("*a") and match-all ("*") elements.
func TestElementForms(t *testing.T) {
	cases := []struct {
		name    string
		elem    string
		value   string
		matches bool
	}{
		{"exact-hit", "/api/v1/items", "/api/v1/items", true},
		{"exact-miss", "/api/v1/items", "/api/v2/items", false},
		{"prefix-hit", "/api/*", "/api/v1/items", true},
		{"prefix-miss", "/api/*", "/web/v1/items", false},
		{"suffix-hit", "*/items", "/api/v1/items", true},
		{"suffix-miss", "*/items", "/api/v1/users", false},
		{"star-all", "*", "/anything", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			mustReplace(t, s, []Policy{
				{
					Name:      "p",
					Namespace: "prod",
					Action:    ActionAllow,
					Rules: []Rule{
						{Operation: &Operation{Paths: []string{tc.elem}}},
					},
				},
			})
			req := baseRequest()
			req.Path = tc.value
			res := mustEvaluate(t, s, req)
			got := res.Decision == DecisionAllow
			if got != tc.matches {
				t.Fatalf("elem %q vs value %q: match=%v, want %v", tc.elem, tc.value, got, tc.matches)
			}
		})
	}
}

// Identity and namespace elements support the same four forms, and
// methods compare exactly and case-sensitively.
func TestIdentityAndNamespaceForms(t *testing.T) {
	cases := []struct {
		name       string
		identities []string
		namespaces []string
		methods    []string
		want       Decision
	}{
		{"identity-exact", []string{"spiffe://cluster.local/ns/prod/sa/web"}, nil, nil, DecisionAllow},
		{"identity-prefix", []string{"spiffe://cluster.local/*"}, nil, nil, DecisionAllow},
		{"identity-suffix", []string{"*/sa/web"}, nil, nil, DecisionAllow},
		{"identity-star", []string{"*"}, nil, nil, DecisionAllow},
		{"identity-miss", []string{"spiffe://other/*"}, nil, nil, DecisionDeny},
		{"namespace-exact", nil, []string{"prod"}, nil, DecisionAllow},
		{"namespace-prefix", nil, []string{"pr*"}, nil, DecisionAllow},
		{"namespace-suffix", nil, []string{"*od"}, nil, DecisionAllow},
		{"namespace-star", nil, []string{"*"}, nil, DecisionAllow},
		{"namespace-miss", nil, []string{"staging"}, nil, DecisionDeny},
		{"method-exact", nil, nil, []string{"GET"}, DecisionAllow},
		{"method-case-sensitive", nil, nil, []string{"get"}, DecisionDeny},
		{"method-no-wildcard", nil, nil, []string{"G*"}, DecisionDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			mustReplace(t, s, []Policy{
				{
					Name:      "p",
					Namespace: "prod",
					Action:    ActionAllow,
					Rules: []Rule{
						{
							From:      &Source{Identities: tc.identities, Namespaces: tc.namespaces},
							Operation: &Operation{Methods: tc.methods},
						},
					},
				},
			})
			res := mustEvaluate(t, s, baseRequest())
			if res.Decision != tc.want {
				t.Fatalf("decision = %s, want %s", res.Decision, tc.want)
			}
		})
	}
}

// Negative sets veto a match; a missing header fails value-requiring
// conditions and satisfies negation-only conditions.
func TestNegationAndMissingHeader(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		{
			Name:      "guarded",
			Namespace: "prod",
			Action:    ActionAllow,
			Rules: []Rule{
				{
					From: &Source{NotIdentities: []string{"spiffe://cluster.local/ns/prod/sa/blocked*"}},
					Operation: &Operation{
						NotPaths: []string{"/admin/*"},
						NotPorts: []int{9090},
					},
					When: []Condition{
						{Key: "X-Tenant", Values: []string{"blue", "green"}, NotValues: []string{"red"}},
						{Key: "X-Debug", NotValues: []string{"1"}}, // header absent: satisfied
					},
				},
			},
		},
	})

	// Baseline: positive header value present, negatives untouched.
	if res := mustEvaluate(t, s, baseRequest()); res.Decision != DecisionAllow {
		t.Fatalf("baseline: want ALLOW, got %s", res.Decision)
	}

	// Negative identity prefix matches: vetoed.
	req := baseRequest()
	req.SourceIdentity = "spiffe://cluster.local/ns/prod/sa/blocked-1"
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negative identity prefix must veto, got %s", res.Decision)
	}
	// A non-matching identity is not vetoed.
	req = baseRequest()
	req.SourceIdentity = "spiffe://cluster.local/ns/prod/sa/reader"
	if res := mustEvaluate(t, s, req); res.Decision != DecisionAllow {
		t.Fatalf("non-matching negative identity must not veto, got %s", res.Decision)
	}

	// Negative path matches: vetoed.
	req = baseRequest()
	req.Path = "/admin/users"
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negative path must veto, got %s", res.Decision)
	}

	// Negative port matches: vetoed.
	req = baseRequest()
	req.Port = 9090
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negative port must veto, got %s", res.Decision)
	}

	// Header value in the negative set: vetoed.
	req = baseRequest()
	req.Headers = map[string][]string{"X-Tenant": {"red"}}
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("negative header value must veto, got %s", res.Decision)
	}

	// Required header missing: condition fails.
	req = baseRequest()
	req.Headers = map[string][]string{}
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("missing required header must fail the rule, got %s", res.Decision)
	}

	// Header names are case-insensitive and multi-valued.
	req = baseRequest()
	req.Headers = map[string][]string{"x-tEnAnt": {"other", "GREEN"}}
	if res := mustEvaluate(t, s, req); res.Decision != DecisionDeny {
		t.Fatalf("values are case-sensitive; GREEN != green, got %s", res.Decision)
	}
	req.Headers = map[string][]string{"x-tEnAnt": {"other", "green"}}
	if res := mustEvaluate(t, s, req); res.Decision != DecisionAllow {
		t.Fatalf("case-insensitive name + multi-value OR must match, got %s", res.Decision)
	}
}

// Audit policies appear in the evidence (sorted by name, then
// namespace) but never change the decision.
func TestAuditDoesNotChangeDecision(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		allowAllPolicy("allow-all", "prod"),
		{Name: "b-audit", Namespace: "prod", Action: ActionAudit, Rules: []Rule{{}}},
		{Name: "a-audit", Namespace: testRootNS, Action: ActionAudit, Rules: []Rule{{}}},
		{Name: "a-audit", Namespace: "prod", Action: ActionAudit, Rules: []Rule{{}}},
		{
			Name:      "c-audit-miss",
			Namespace: "prod",
			Action:    ActionAudit,
			Rules:     []Rule{{Operation: &Operation{Methods: []string{"POST"}}}},
		},
	})
	res := mustEvaluate(t, s, baseRequest())
	if res.Decision != DecisionAllow {
		t.Fatalf("audit must not change the decision, got %s", res.Decision)
	}
	want := []PolicyRef{
		{Name: "a-audit", Namespace: testRootNS, Action: ActionAudit},
		{Name: "a-audit", Namespace: "prod", Action: ActionAudit},
		{Name: "b-audit", Namespace: "prod", Action: ActionAudit},
	}
	if !reflect.DeepEqual(res.Evidence.AuditPolicies, want) {
		t.Fatalf("audit policies = %+v, want %+v", res.Evidence.AuditPolicies, want)
	}

	// Audit policies alone do not count as allow policies: default
	// allow still applies and the decision part stays empty.
	s2 := newTestStore(t)
	mustReplace(t, s2, []Policy{
		{Name: "only-audit", Namespace: "prod", Action: ActionAudit, Rules: []Rule{{}}},
	})
	res = mustEvaluate(t, s2, baseRequest())
	if res.Decision != DecisionAllow || len(res.Evidence.DecisionPolicies) != 0 {
		t.Fatalf("audit-only set: got %s %+v", res.Decision, res.Evidence.DecisionPolicies)
	}
}

// Evidence carries the policy-set version the evaluation used, and
// matched deny policies are all listed.
func TestEvidenceVersionAndDenyListing(t *testing.T) {
	s := newTestStore(t)
	v1 := mustReplace(t, s, []Policy{
		{Name: "deny-a", Namespace: "prod", Action: ActionDeny, Rules: []Rule{{}}},
		{Name: "deny-b", Namespace: testRootNS, Action: ActionDeny, Rules: []Rule{{}}},
	})
	res := mustEvaluate(t, s, baseRequest())
	if res.Evidence.Version != v1 {
		t.Fatalf("evidence version = %d, want %d", res.Evidence.Version, v1)
	}
	want := []PolicyRef{
		{Name: "deny-a", Namespace: "prod", Action: ActionDeny},
		{Name: "deny-b", Namespace: testRootNS, Action: ActionDeny},
	}
	if !reflect.DeepEqual(res.Evidence.DecisionPolicies, want) {
		t.Fatalf("decision policies = %+v, want %+v", res.Evidence.DecisionPolicies, want)
	}

	v2 := mustReplace(t, s, []Policy{allowAllPolicy("allow-all", "prod")})
	if v2 != v1+1 {
		t.Fatalf("version must increase monotonically: v1=%d v2=%d", v1, v2)
	}
	res = mustEvaluate(t, s, baseRequest())
	if res.Evidence.Version != v2 || res.Decision != DecisionAllow {
		t.Fatalf("after replace: version=%d decision=%s", res.Evidence.Version, res.Decision)
	}
}

// Invalid policy sets are rejected as a whole: the version stays put
// and the previously installed set keeps serving evaluations.
func TestInvalidPolicySetRejectedAtomically(t *testing.T) {
	cases := []struct {
		name     string
		policies []Policy
		wantIdx  int
	}{
		{
			name: "duplicate-name-same-namespace",
			policies: []Policy{
				allowAllPolicy("dup", "prod"),
				{Name: "dup", Namespace: "prod", Action: ActionDeny, Rules: []Rule{{}}},
			},
			wantIdx: 1,
		},
		{
			name: "illegal-action",
			policies: []Policy{
				{Name: "p", Namespace: "prod", Action: "SHADOW", Rules: []Rule{{}}},
			},
			wantIdx: 0,
		},
		{
			name: "empty-element",
			policies: []Policy{
				{
					Name: "p", Namespace: "prod", Action: ActionAllow,
					Rules: []Rule{{From: &Source{Identities: []string{"ok", ""}}}},
				},
			},
			wantIdx: 0,
		},
		{
			name: "star-both-ends",
			policies: []Policy{
				{
					Name: "p", Namespace: "prod", Action: ActionAllow,
					Rules: []Rule{{Operation: &Operation{Paths: []string{"*/api/*"}}}},
				},
			},
			wantIdx: 0,
		},
		{
			name: "star-in-middle",
			policies: []Policy{
				{
					Name: "p", Namespace: "prod", Action: ActionAllow,
					Rules: []Rule{{From: &Source{Namespaces: []string{"pr*d*"}}}},
				},
			},
			wantIdx: 0,
		},
		{
			name: "port-out-of-range",
			policies: []Policy{
				{
					Name: "p", Namespace: "prod", Action: ActionAllow,
					Rules: []Rule{{Operation: &Operation{Ports: []int{0}}}},
				},
			},
			wantIdx: 0,
		},
		{
			name: "port-too-large",
			policies: []Policy{
				{
					Name: "p", Namespace: "prod", Action: ActionAllow,
					Rules: []Rule{{Operation: &Operation{NotPorts: []int{65536}}}},
				},
			},
			wantIdx: 0,
		},
		{
			name: "first-problem-in-submission-order",
			policies: []Policy{
				allowAllPolicy("ok", "prod"),
				{Name: "bad-action", Namespace: "prod", Action: "NOPE"},
				{
					Name: "bad-port", Namespace: "prod", Action: ActionAllow,
					Rules: []Rule{{Operation: &Operation{Ports: []int{-1}}}},
				},
			},
			wantIdx: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			good := mustReplace(t, s, []Policy{allowAllPolicy("allow-all", "prod")})
			v, err := s.ReplaceAll(tc.policies)
			if err == nil {
				t.Fatalf("want error, got version %d", v)
			}
			var perr *Error
			if !errorsAs(err, &perr) || perr.Category != ErrCategoryInvalidPolicySet {
				t.Fatalf("error category = %v, want invalid_policy_set", err)
			}
			if perr.PolicyIndex != tc.wantIdx {
				t.Fatalf("policy index = %d, want %d (%v)", perr.PolicyIndex, tc.wantIdx, err)
			}
			if v != good || s.Version() != good {
				t.Fatalf("failed replace must not change the version: got %d, want %d", v, good)
			}
			res := mustEvaluate(t, s, baseRequest())
			if res.Decision != DecisionAllow || res.Evidence.Version != good {
				t.Fatalf("old set must keep serving: %s v%d", res.Decision, res.Evidence.Version)
			}
			t.Logf("rejected as expected: %v", err)
		})
	}
}

// Same name in different namespaces is legal; a lone "*" element is
// legal; boundary ports are legal.
func TestValidPolicySetAccepted(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{
		{Name: "same", Namespace: "prod", Action: ActionAllow, Rules: []Rule{{}}},
		{Name: "same", Namespace: "staging", Action: ActionDeny, Rules: []Rule{{}}},
		{
			Name: "wildcards", Namespace: "prod", Action: ActionAudit,
			Rules: []Rule{
				{
					From:      &Source{Identities: []string{"*"}, Namespaces: []string{"a*", "*b"}},
					Operation: &Operation{Paths: []string{"*"}, Ports: []int{1, 65535}},
				},
			},
		},
	})
}

// Illegal requests fail with the highest-priority error category and
// evaluation never fails because of policy content.
func TestInvalidRequestCategory(t *testing.T) {
	s := newTestStore(t)
	mustReplace(t, s, []Policy{allowAllPolicy("allow-all", "prod")})

	bad := []func(*Request){
		func(r *Request) { r.SourceNamespace = "" },
		func(r *Request) { r.TargetNamespace = "" },
		func(r *Request) { r.Method = "" },
		func(r *Request) { r.Path = "" },
		func(r *Request) { r.Port = 0 },
		func(r *Request) { r.Port = 70000 },
		func(r *Request) { r.Headers = map[string][]string{"": {"x"}} },
	}
	for i, mutate := range bad {
		req := baseRequest()
		mutate(req)
		_, err := s.Evaluate(req)
		if err == nil {
			t.Fatalf("case %d: want error", i)
		}
		var perr *Error
		if !errorsAs(err, &perr) || perr.Category != ErrCategoryInvalidArgument {
			t.Fatalf("case %d: category = %v, want invalid_argument", i, err)
		}
		t.Logf("case %d rejected as expected: %v", i, err)
	}
	if _, err := s.Evaluate(nil); err == nil {
		t.Fatal("nil request must be rejected")
	}
}

// errorsAs is a tiny local helper to keep the test imports explicit.
func errorsAs(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
