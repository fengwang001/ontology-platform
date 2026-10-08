package authz

import (
	"strings"
	"testing"
)

func TestInvalidRequestRejected(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"empty source namespace", func(r *Request) { r.SourceNamespace = "" }},
		{"empty target namespace", func(r *Request) { r.TargetNamespace = "" }},
		{"empty method", func(r *Request) { r.Method = "" }},
		{"port zero", func(r *Request) { r.Port = 0 }},
		{"port too large", func(r *Request) { r.Port = 65536 }},
		{"empty header name", func(r *Request) { r.Headers = map[string][]string{"": {"v"}} }},
		{"empty header value", func(r *Request) { r.Headers = map[string][]string{"X-A": {""}} }},
	}
	for _, c := range cases {
		req := baseReq()
		c.mutate(&req)
		_, err := s.Evaluate(req)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
			continue
		}
		ae, ok := err.(*Error)
		if !ok || ae.Kind != ErrKindInvalidArgument {
			t.Errorf("%s: expected ErrKindInvalidArgument, got %v", c.name, err)
		}
	}
}

func TestInvalidPolicySetRejectedAtomically(t *testing.T) {
	good := allowPolicy("good", "prod", Rule{})
	cases := []struct {
		name    string
		policy  Policy
		wantSub string
	}{
		{"empty name", Policy{Namespace: "prod", Action: ActionAllow}, "Name"},
		{"empty namespace", Policy{Name: "p", Action: ActionAllow}, "Namespace"},
		{"illegal action", Policy{Name: "p", Namespace: "prod", Action: Action(42)}, "Action"},
		{"empty identity element", allowPolicy("p", "prod", Rule{Source: &SourceSpec{Identities: []string{""}}}), "Identities"},
		{"star in middle", allowPolicy("p", "prod", Rule{Source: &SourceSpec{Namespaces: []string{"a*b"}}}), "Namespaces"},
		{"star both ends", allowPolicy("p", "prod", Rule{Operation: &OperationSpec{Paths: []string{"*a*"}}}), "Paths"},
		{"double star", allowPolicy("p", "prod", Rule{Operation: &OperationSpec{Paths: []string{"**"}}}), "Paths"},
		{"empty method", allowPolicy("p", "prod", Rule{Operation: &OperationSpec{Methods: []string{""}}}), "Methods"},
		{"port out of range", allowPolicy("p", "prod", Rule{Operation: &OperationSpec{Ports: []int{70000}}}), "Ports"},
		{"port zero", denyPolicy("p", "prod", Rule{Operation: &OperationSpec{NotPorts: []int{0}}}), "NotPorts"},
		{"empty selector key", Policy{Name: "p", Namespace: "prod", Action: ActionAllow, Selector: map[string]string{"": "v"}}, "Selector"},
		{"empty selector value", Policy{Name: "p", Namespace: "prod", Action: ActionAllow, Selector: map[string]string{"k": ""}}, "Selector"},
		{"empty condition header", allowPolicy("p", "prod", Rule{Conditions: []Condition{{Header: "", Values: []string{"v"}}}}), "Header"},
		{"empty condition value", allowPolicy("p", "prod", Rule{Conditions: []Condition{{Header: "h", Values: []string{""}}}}), "Values"},
	}
	for _, c := range cases {
		s := newStore(t)
		v0 := s.Version()
		_, err := s.ReplaceAll([]Policy{good, c.policy})
		if err == nil {
			t.Errorf("%s: expected rejection", c.name)
			continue
		}
		pe, ok := err.(*Error)
		if !ok || pe.Kind != ErrKindInvalidPolicySet {
			t.Errorf("%s: expected ErrKindInvalidPolicySet, got %v", c.name, err)
			continue
		}
		if !strings.Contains(pe.Field, c.wantSub) {
			t.Errorf("%s: error field %q does not mention %q", c.name, pe.Field, c.wantSub)
		}
		if pe.PolicyIndex != 1 {
			t.Errorf("%s: expected PolicyIndex 1, got %d", c.name, pe.PolicyIndex)
		}
		if s.Version() != v0 {
			t.Errorf("%s: version changed on rejected replacement", c.name)
		}
		// The whole replacement must not take effect: previous (empty) set
		// still visible.
		res := mustEval(t, s, baseReq())
		if res.Version != v0 || len(res.DecisionPolicies) != 0 {
			t.Errorf("%s: rejected replacement leaked into evaluation: %+v", c.name, res)
		}
	}
}

func TestDuplicateNameInSameNamespace(t *testing.T) {
	s := newStore(t)
	_, err := s.ReplaceAll([]Policy{
		allowPolicy("p", "prod", Rule{}),
		denyPolicy("p", "prod", Rule{}),
	})
	if err == nil {
		t.Fatal("duplicate name in same namespace must be rejected")
	}
	// Same name in different namespaces is legal.
	s2 := newStore(t)
	if _, err := s2.ReplaceAll([]Policy{
		allowPolicy("p", "prod", Rule{}),
		allowPolicy("p", "other", Rule{}),
	}); err != nil {
		t.Fatalf("same name across namespaces must be legal: %v", err)
	}
}

func TestFirstProblemInSubmissionOrderReported(t *testing.T) {
	s := newStore(t)
	bad1 := allowPolicy("first", "prod", Rule{Operation: &OperationSpec{Ports: []int{-1}}})
	bad2 := allowPolicy("second", "prod", Rule{Operation: &OperationSpec{Methods: []string{""}}})
	_, err := s.ReplaceAll([]Policy{bad1, bad2})
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %v", err)
	}
	if pe.PolicyIndex != 0 || pe.Policy != "first" {
		t.Fatalf("must report the first problem in submission order; got %+v", pe)
	}
}

func TestVersionMonotonicOnSuccess(t *testing.T) {
	s := newStore(t)
	v1 := mustReplace(t, s, allowPolicy("a", "prod", Rule{}))
	v2 := mustReplace(t, s, allowPolicy("b", "prod", Rule{}))
	if v1 != 1 || v2 != 2 {
		t.Fatalf("version must increase by one per successful replacement; got %d, %d", v1, v2)
	}
	if _, err := s.ReplaceAll([]Policy{allowPolicy("bad", "prod", Rule{Operation: &OperationSpec{Ports: []int{0}}})}); err == nil {
		t.Fatal("expected rejection")
	}
	if s.Version() != v2 {
		t.Fatalf("failed replacement must not bump the version; got %d", s.Version())
	}
	v3 := mustReplace(t, s)
	if v3 != 3 {
		t.Fatalf("empty set is a legal replacement; got version %d", v3)
	}
}

func TestNewStoreRequiresRootNamespace(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("empty root namespace must be rejected")
	}
}
