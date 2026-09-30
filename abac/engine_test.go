package abac

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func mustRegister(t *testing.T, e *Engine, policies ...Policy) {
	t.Helper()
	for _, p := range policies {
		if err := e.RegisterPolicy(p); err != nil {
			t.Fatalf("RegisterPolicy(%q): %v", p.ID, err)
		}
	}
}

// readPolicy permits subjects in dept "eng" with level >= 3 to access
// resources in dept "eng" from an internal network.
func readPolicy() Policy {
	return Policy{
		ID:     "dept-read",
		Effect: EffectPermit,
		Conditions: []Condition{
			{Scope: ScopeSubject, Key: "dept", Op: OpEq, Value: "eng"},
			{Scope: ScopeResource, Key: "dept", Op: OpEq, Value: "eng"},
			{Scope: ScopeSubject, Key: "level", Op: OpGte, Value: 3},
			{Scope: ScopeEnvironment, Key: "network", Op: OpIn,
				Value: []string{"internal", "vpn"}},
		},
	}
}

func denyInternPolicy() Policy {
	return Policy{
		ID:     "deny-intern",
		Effect: EffectDeny,
		Conditions: []Condition{
			{Scope: ScopeSubject, Key: "role", Op: OpEq, Value: "intern"},
		},
	}
}

func validRequest() Request {
	return Request{
		Subject:     Attributes{"dept": "eng", "level": 5},
		Resource:    Attributes{"dept": "eng"},
		Environment: Attributes{"network": "internal"},
		Action:      "read",
	}
}

func TestAttributeConditionEvaluation(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	mustRegister(t, e, readPolicy())

	cases := []struct {
		name    string
		mutate  func(r *Request)
		allowed bool
	}{
		{"all conditions satisfied", func(r *Request) {}, true},
		{"subject dept mismatch", func(r *Request) { r.Subject["dept"] = "ops" }, false},
		{"resource dept mismatch", func(r *Request) { r.Resource["dept"] = "ops" }, false},
		{"level below threshold", func(r *Request) { r.Subject["level"] = 2 }, false},
		{"network not in list", func(r *Request) { r.Environment["network"] = "public" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.mutate(&req)
			dec := e.Evaluate(req)
			if dec.Allowed != tc.allowed {
				t.Fatalf("allowed=%v, want %v (reason=%s)", dec.Allowed, tc.allowed, dec.Reason)
			}
			wantReason := ReasonNoApplicablePermit
			if tc.allowed {
				wantReason = ReasonPermitted
			}
			if dec.Reason != wantReason {
				t.Fatalf("reason=%s, want %s", dec.Reason, wantReason)
			}
		})
	}
}

func TestDenyOverridesPermit(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	// Register permit first, then deny; order must not matter.
	mustRegister(t, e, readPolicy(), denyInternPolicy())

	req := validRequest()
	req.Subject["role"] = "intern"
	dec := e.Evaluate(req)
	if dec.Allowed {
		t.Fatal("deny policy must override matching permit policy")
	}
	if dec.Reason != ReasonDeniedByPolicy {
		t.Fatalf("reason=%s, want %s", dec.Reason, ReasonDeniedByPolicy)
	}
	wantMatched := []string{"deny-intern", "dept-read"}
	if !reflect.DeepEqual(dec.MatchedPolicies, wantMatched) {
		t.Fatalf("matched=%v, want %v", dec.MatchedPolicies, wantMatched)
	}

	// Without the deny condition, the permit applies again.
	req.Subject["role"] = "staff"
	if dec := e.Evaluate(req); !dec.Allowed {
		t.Fatalf("staff should be permitted, got reason=%s", dec.Reason)
	}
}

func TestMissingAttributeMakesPolicyNotApplicable(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	mustRegister(t, e, readPolicy())

	missing := []struct {
		name   string
		mutate func(r *Request)
	}{
		{"missing subject dept", func(r *Request) { delete(r.Subject, "dept") }},
		{"missing subject level", func(r *Request) { delete(r.Subject, "level") }},
		{"missing resource dept", func(r *Request) { delete(r.Resource, "dept") }},
		{"missing environment network", func(r *Request) { delete(r.Environment, "network") }},
		{"nil subject attributes", func(r *Request) { r.Subject = nil }},
		{"nil environment attributes", func(r *Request) { r.Environment = nil }},
	}
	for _, tc := range missing {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.mutate(&req)
			dec := e.Evaluate(req)
			if dec.Allowed {
				t.Fatal("missing attribute must not satisfy the policy")
			}
			if dec.Reason != ReasonNoApplicablePermit {
				t.Fatalf("reason=%s, want %s", dec.Reason, ReasonNoApplicablePermit)
			}
			if len(dec.MatchedPolicies) != 0 {
				t.Fatalf("matched=%v, want none", dec.MatchedPolicies)
			}
		})
	}

	// A missing attribute must not be treated as a false/zero value:
	// a deny policy on a numeric comparison must not fire when the
	// attribute is absent.
	var buf2 bytes.Buffer
	e2 := NewEngine(testLogger(&buf2))
	mustRegister(t, e2,
		Policy{
			ID:     "permit-all-eng",
			Effect: EffectPermit,
			Conditions: []Condition{
				{Scope: ScopeSubject, Key: "dept", Op: OpEq, Value: "eng"},
			},
		},
		Policy{
			ID:     "deny-low-level",
			Effect: EffectDeny,
			Conditions: []Condition{
				{Scope: ScopeSubject, Key: "level", Op: OpLt, Value: 3},
			},
		},
	)
	req := Request{Subject: Attributes{"dept": "eng"}, Resource: Attributes{}}
	dec := e2.Evaluate(req)
	if !dec.Allowed {
		t.Fatalf("missing level must not trigger deny-low-level, got reason=%s", dec.Reason)
	}
	// Sanity: an actual low level does trigger the deny.
	req.Subject["level"] = 1
	if dec := e2.Evaluate(req); dec.Allowed || dec.Reason != ReasonDeniedByPolicy {
		t.Fatalf("level=1 should be denied, got allowed=%v reason=%s", dec.Allowed, dec.Reason)
	}
}

func TestNoResourceExistenceLeak(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	mustRegister(t, e, readPolicy(), denyInternPolicy())

	// Nonexistent resource: Resource is nil.
	probing := validRequest()
	probing.Resource = nil
	decProbe := e.Evaluate(probing)

	// Existing resource the subject has no permit for.
	denied := validRequest()
	denied.Resource = Attributes{"dept": "finance"}
	decDenied := e.Evaluate(denied)

	if decProbe.Allowed {
		t.Fatal("request for nonexistent resource must be denied")
	}
	if !reflect.DeepEqual(decProbe, decDenied) {
		t.Fatalf("decisions differ: probe=%+v denied=%+v; existence must be indistinguishable",
			decProbe, decDenied)
	}
	if decProbe.Reason != ReasonNoApplicablePermit {
		t.Fatalf("reason=%s, want %s", decProbe.Reason, ReasonNoApplicablePermit)
	}

	// Even a subject matching a deny policy must not learn existence.
	probing.Subject["role"] = "intern"
	decProbe2 := e.Evaluate(probing)
	denied.Subject["role"] = "intern"
	decDenied2 := e.Evaluate(denied)
	if decProbe2.Allowed || decProbe2.Reason != ReasonNoApplicablePermit {
		t.Fatalf("probe with deny-matching subject: got %+v", decProbe2)
	}
	if decDenied2.Reason != ReasonDeniedByPolicy {
		t.Fatalf("existing resource with deny match: got %+v", decDenied2)
	}
}

func TestConcurrentEvaluationIsConsistent(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	mustRegister(t, e, readPolicy(), denyInternPolicy())

	reqs := []Request{
		validRequest(),
		func() Request { r := validRequest(); r.Subject["role"] = "intern"; return r }(),
		func() Request { r := validRequest(); r.Resource = nil; return r }(),
		func() Request { r := validRequest(); r.Subject["level"] = 1; return r }(),
	}
	want := make([]Decision, len(reqs))
	for i, r := range reqs {
		want[i] = e.Evaluate(r)
	}

	const workers = 16
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan string, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for n := 0; n < rounds; n++ {
				i := (w + n) % len(reqs)
				got := e.Evaluate(reqs[i])
				if !reflect.DeepEqual(got, want[i]) {
					errs <- "inconsistent decision"
				}
			}
		}(w)
	}
	// Concurrent registration must not disturb evaluations.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 50; n++ {
			_ = e.RegisterPolicy(Policy{
				ID:     "late-" + string(rune('a'+n%26)),
				Effect: EffectDeny,
				Conditions: []Condition{
					{Scope: ScopeSubject, Key: "never", Op: OpEq, Value: n},
				},
			})
		}
	}()
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

func TestRegistrationOrderDoesNotAffectDecision(t *testing.T) {
	policies := []Policy{
		readPolicy(),
		denyInternPolicy(),
		{
			ID:     "permit-vpn",
			Effect: EffectPermit,
			Conditions: []Condition{
				{Scope: ScopeEnvironment, Key: "network", Op: OpEq, Value: "vpn"},
			},
		},
		{
			ID:     "deny-public",
			Effect: EffectDeny,
			Conditions: []Condition{
				{Scope: ScopeEnvironment, Key: "network", Op: OpEq, Value: "public"},
			},
		},
	}
	req := validRequest()
	req.Subject["role"] = "intern"

	var reference *Decision
	orders := [][]int{
		{0, 1, 2, 3},
		{3, 2, 1, 0},
		{1, 3, 0, 2},
		{2, 0, 3, 1},
	}
	for _, order := range orders {
		e := NewEngine(nil)
		for _, i := range order {
			mustRegister(t, e, policies[i])
		}
		dec := e.Evaluate(req)
		if reference == nil {
			reference = &dec
			continue
		}
		if !reflect.DeepEqual(dec, *reference) {
			t.Fatalf("order %v: got %+v, want %+v", order, dec, *reference)
		}
	}
	if reference == nil || reference.Allowed || reference.Reason != ReasonDeniedByPolicy {
		t.Fatalf("reference decision wrong: %+v", reference)
	}
}

func TestRejectedRegistrationDoesNotChangePolicies(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	mustRegister(t, e, readPolicy())
	before := e.Policies()

	invalid := []Policy{
		{ID: "", Effect: EffectPermit, Conditions: []Condition{
			{Scope: ScopeSubject, Key: "a", Op: OpEq, Value: 1}}},
		{ID: "no-conditions", Effect: EffectPermit},
		{ID: "empty-key", Effect: EffectDeny, Conditions: []Condition{
			{Scope: ScopeSubject, Key: "", Op: OpEq, Value: 1}}},
		{ID: "bad-scope", Effect: EffectDeny, Conditions: []Condition{
			{Scope: Scope(99), Key: "a", Op: OpEq, Value: 1}}},
		{ID: "bad-op", Effect: EffectDeny, Conditions: []Condition{
			{Scope: ScopeSubject, Key: "a", Op: Operator(99), Value: 1}}},
		{ID: "bad-effect", Effect: Effect(99), Conditions: []Condition{
			{Scope: ScopeSubject, Key: "a", Op: OpEq, Value: 1}}},
		readPolicy(), // duplicate ID
	}
	for _, p := range invalid {
		if err := e.RegisterPolicy(p); err == nil {
			t.Fatalf("policy %q: expected registration error", p.ID)
		}
	}
	after := e.Policies()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("policy set changed by rejected registrations: %v -> %v", before, after)
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(after))
	}
}

func TestDecisionLogContainsBasis(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(testLogger(&buf))
	mustRegister(t, e, readPolicy(), denyInternPolicy())

	req := validRequest()
	req.Subject["role"] = "intern"
	e.Evaluate(req)

	logs := buf.String()
	for _, want := range []string{"dept-read", "deny-intern", "denied-by-policy", "eng", "read"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q:\n%s", want, logs)
		}
	}
	// The decision log entry must be structured and carry the basis.
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("log is not JSON: %v", err)
	}
	if entry["allowed"] != false {
		t.Fatalf("log allowed=%v, want false", entry["allowed"])
	}
	if _, ok := entry["matchedPolicies"]; !ok {
		t.Fatal("log missing matchedPolicies")
	}
	if _, ok := entry["subject"]; !ok {
		t.Fatal("log missing subject")
	}
	if _, ok := entry["resource"]; !ok {
		t.Fatal("log missing resource")
	}
}
