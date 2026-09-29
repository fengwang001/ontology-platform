package abac

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

func TestEvaluateCondition(t *testing.T) {
	attrs := Attributes{
		"role":    "admin",
		"level":   int64(5),
		"score":   3.5,
		"active":  true,
		"tags":    []any{"a", "b"},
		"missing": "x",
	}
	cases := []struct {
		name      string
		c         Condition
		wantMatch bool
		wantDec   bool
	}{
		{"eq string", Condition{Key: "role", Op: OpEqual, Value: "admin"}, true, true},
		{"eq string mismatch", Condition{Key: "role", Op: OpEqual, Value: "user"}, false, true},
		{"ne", Condition{Key: "role", Op: OpNotEqual, Value: "user"}, true, true},
		{"lt numeric cross type", Condition{Key: "level", Op: OpLess, Value: 6}, true, true},
		{"ge float", Condition{Key: "score", Op: OpGreaterEqual, Value: 3.5}, true, true},
		{"contains", Condition{Key: "role", Op: OpStringContains, Value: "dm"}, true, true},
		{"prefix", Condition{Key: "role", Op: OpStringPrefix, Value: "ad"}, true, true},
		{"suffix", Condition{Key: "role", Op: OpStringSuffix, Value: "in"}, true, true},
		{"in hit", Condition{Key: "role", Op: OpIn, Value: []any{"admin", "ops"}}, true, true},
		{"in miss", Condition{Key: "role", Op: OpIn, Value: []any{"guest"}}, false, true},
		{"exists hit", Condition{Key: "active", Op: OpExists}, true, true},
		{"exists miss decidable", Condition{Key: "ghost", Op: OpExists}, false, true},
		{"not exists hit", Condition{Key: "ghost", Op: OpNotExists}, true, true},
		{"missing referenced attr indeterminate", Condition{Key: "ghost", Op: OpEqual, Value: 1}, false, false},
		{"type mismatch treated as no match", Condition{Key: "active", Op: OpLess, Value: 1}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dec := evaluateCondition(tc.c, attrs)
			if got != tc.wantMatch || dec != tc.wantDec {
				t.Fatalf("got (%v,%v), want (%v,%v)", got, dec, tc.wantMatch, tc.wantDec)
			}
		})
	}
}

var denyAdmin = Policy{
	ID:      "p-deny-admin",
	Effect:  EffectDeny,
	Subject: []Condition{{Key: "role", Op: OpEqual, Value: "admin"}},
}

var allowRead = Policy{
	ID:       "p-allow-read",
	Effect:   EffectAllow,
	Subject:  []Condition{{Key: "role", Op: OpIn, Value: []any{"admin", "analyst"}}},
	Resource: []Condition{{Key: "kind", Op: OpEqual, Value: "report"}},
}

func TestDenyOverridesAllow(t *testing.T) {
	eng := NewEngine()
	mustRegister(t, eng, allowRead, denyAdmin)

	req := Request{
		Action:   "read",
		Subject:  Attributes{"role": "admin"},
		Resource: Attributes{"kind": "report"},
	}
	pub, audit := eng.Evaluate(context.Background(), req)
	if pub.Allowed {
		t.Fatal("deny policy must override allow policy")
	}
	if audit.Reason != ReasonDenyByPolicy || audit.DenyPolicy != "p-deny-admin" {
		t.Fatalf("unexpected audit: %+v", audit)
	}
	if audit.AllowPolicy != "p-allow-read" {
		t.Fatalf("allow policy should be recorded as overridden, got %q", audit.AllowPolicy)
	}
	if pub.Message != PublicDenyMessage {
		t.Fatalf("public message leaks details: %q", pub.Message)
	}
}

func TestAllowWhenNoDeny(t *testing.T) {
	eng := NewEngine()
	mustRegister(t, eng, allowRead)
	req := Request{
		Action:      "read",
		Subject:     Attributes{"role": "analyst"},
		Resource:    Attributes{"kind": "report"},
		Environment: Attributes{"ip": "10.0.0.1"},
	}
	pub, audit := eng.Evaluate(context.Background(), req)
	if !pub.Allowed || audit.AllowPolicy != "p-allow-read" {
		t.Fatalf("expected allow, got %+v %+v", pub, audit)
	}
}

func TestMissingAttributeIsIndeterminateNotFalse(t *testing.T) {
	eng := NewEngine()
	mustRegister(t, eng, Policy{
		ID:       "p-needs-clearance",
		Effect:   EffectAllow,
		Resource: []Condition{{Key: "clearance", Op: OpGreaterEqual, Value: 3}},
	})
	req := Request{
		Action:   "read",
		Subject:  Attributes{"role": "analyst"},
		Resource: Attributes{"kind": "report"}, // 没有 clearance 属性
	}
	pub, audit := eng.Evaluate(context.Background(), req)
	if pub.Allowed {
		t.Fatal("missing referenced attribute must not be treated as false-match allow")
	}
	if audit.Reason != ReasonMissingAttr {
		t.Fatalf("want %s, got %s", ReasonMissingAttr, audit.Reason)
	}
	if len(audit.Indeterminate) != 1 || audit.Indeterminate[0] != "p-needs-clearance" {
		t.Fatalf("unexpected indeterminate list: %v", audit.Indeterminate)
	}
}

func TestExistsOpHandlesMissingExplicitly(t *testing.T) {
	eng := NewEngine()
	mustRegister(t, eng, Policy{
		ID:       "p-public-resource",
		Effect:   EffectAllow,
		Resource: []Condition{{Key: "owner", Op: OpNotExists}},
	})
	req := Request{Action: "read", Resource: Attributes{"kind": "public"}}
	pub, audit := eng.Evaluate(context.Background(), req)
	if !pub.Allowed {
		t.Fatalf("not_exists on missing attribute should match: %+v", audit)
	}
}

func TestNoExistenceLeak(t *testing.T) {
	eng := NewEngine()
	mustRegister(t, eng, Policy{
		ID:       "p-owner-only",
		Effect:   EffectAllow,
		Subject:  []Condition{{Key: "user", Op: OpEqual, Value: "alice"}},
		Resource: []Condition{{Key: "owner", Op: OpEqual, Value: "alice"}},
	})

	hidden := Request{
		Action:   "read",
		Subject:  Attributes{"user": "bob"},
		Resource: Attributes{"owner": "alice", "kind": "secret"},
	}
	missing := Request{
		Action:   "read",
		Subject:  Attributes{"user": "bob"},
		Resource: nil, // 资源不存在
	}

	pub1, audit1 := eng.Evaluate(context.Background(), hidden)
	pub2, audit2 := eng.Evaluate(context.Background(), missing)

	if pub1.Allowed || pub2.Allowed {
		t.Fatal("both cases must be denied")
	}
	if pub1.Message != pub2.Message || pub1.Action != pub2.Action {
		t.Fatalf("public decisions must be identical: %+v vs %+v", pub1, pub2)
	}
	if audit1.Reason != ReasonNoApplicable || audit2.Reason != ReasonNoApplicable {
		t.Fatalf("both should default-deny, got %s / %s", audit1.Reason, audit2.Reason)
	}
	if audit1.ResourcePresent == audit2.ResourcePresent {
		t.Fatal("audit must retain resource presence for internal forensics")
	}
}

func TestRegistrationOrderIndependent(t *testing.T) {
	orderA := []Policy{allowRead, denyAdmin}
	orderB := []Policy{denyAdmin, allowRead}

	results := func(policies []Policy) (Decision, AuditDecision) {
		eng := NewEngine()
		mustRegister(t, eng, policies...)
		req := Request{
			Action:   "read",
			Subject:  Attributes{"role": "admin"},
			Resource: Attributes{"kind": "report"},
		}
		return eng.Evaluate(context.Background(), req)
	}

	d1, a1 := results(orderA)
	d2, a2 := results(orderB)
	if d1 != d2 {
		t.Fatalf("public decisions differ: %+v vs %+v", d1, d2)
	}
	if a1.Reason != a2.Reason || a1.DenyPolicy != a2.DenyPolicy ||
		a1.AllowPolicy != a2.AllowPolicy || !equalStrings(a1.Matched, a2.Matched) {
		t.Fatalf("audit decisions differ: %+v vs %+v", a1, a2)
	}
}

func TestConcurrentEvaluationConsistency(t *testing.T) {
	eng := NewEngine()
	mustRegister(t, eng, allowRead, denyAdmin)
	req := Request{
		Action:   "read",
		Subject:  Attributes{"role": "admin"},
		Resource: Attributes{"kind": "report"},
	}

	const goroutines = 64
	var wg sync.WaitGroup
	results := make([]AuditDecision, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, results[idx] = eng.Evaluate(context.Background(), req)
		}(i)
	}
	wg.Wait()

	for i := 1; i < goroutines; i++ {
		if results[i].Reason != results[0].Reason ||
			results[i].DenyPolicy != results[0].DenyPolicy ||
			!equalStrings(results[i].Matched, results[0].Matched) {
			t.Fatalf("inconsistent concurrent decision at %d: %+v", i, results[i])
		}
	}

	before := len(eng.Policies())
	eng.Evaluate(context.Background(), req)
	if len(eng.Policies()) != before {
		t.Fatal("evaluation must not mutate registered policies")
	}
}

type captureLogger struct{ entries []LogEntry }

func (c *captureLogger) LogDecision(_ context.Context, e LogEntry) {
	c.entries = append(c.entries, e)
}

func TestAuditLogContents(t *testing.T) {
	capLog := &captureLogger{}
	eng := NewEngine(WithLogger(capLog))
	mustRegister(t, eng, allowRead, denyAdmin)
	req := Request{
		Action:   "read",
		Subject:  Attributes{"role": "admin"},
		Resource: Attributes{"kind": "report"},
	}
	eng.Evaluate(context.Background(), req)

	if len(capLog.entries) != 1 {
		t.Fatalf("want 1 log entry, got %d", len(capLog.entries))
	}
	e := capLog.entries[0]
	if e.Subject["role"] != "admin" || e.Resource["kind"] != "report" {
		t.Fatalf("log must include subject and resource: %+v", e)
	}
	if e.Allowed || e.Reason != ReasonDenyByPolicy || e.DenyPolicy != "p-deny-admin" {
		t.Fatalf("log decision fields wrong: %+v", e)
	}
	if !strings.Contains(e.Basis, "deny-overrides") {
		t.Fatalf("log basis must explain combination, got %q", e.Basis)
	}
}

func TestSlogLoggerPrintsFields(t *testing.T) {
	var buf bytes.Buffer
	eng := NewEngine(WithLogger(NewSlogLoggerWithWriter(&buf)))
	mustRegister(t, eng, allowRead)
	req := Request{
		Action:   "read",
		Subject:  Attributes{"role": "analyst"},
		Resource: Attributes{"kind": "report"},
	}
	eng.Evaluate(context.Background(), req)
	out := buf.String()
	for _, want := range []string{"subject", "resource", "matched_policies", "basis", "abac decision"} {
		if !strings.Contains(out, want) {
			t.Fatalf("slog output missing %q:\n%s", want, out)
		}
	}
}

func TestRegisterValidation(t *testing.T) {
	eng := NewEngine()
	if err := eng.Register(Policy{ID: " ", Effect: EffectAllow}); err != ErrPolicyIDRequired {
		t.Fatalf("want ErrPolicyIDRequired, got %v", err)
	}
	if err := eng.Register(Policy{ID: "x", Effect: "maybe"}); err != ErrInvalidEffect {
		t.Fatalf("want ErrInvalidEffect, got %v", err)
	}
	if err := eng.Register(Policy{
		ID:      "x",
		Effect:  EffectAllow,
		Subject: []Condition{{Key: "a", Op: "??"}},
	}); err != ErrInvalidOp {
		t.Fatalf("want ErrInvalidOp, got %v", err)
	}
	mustRegister(t, eng, allowRead)
	if err := eng.Register(allowRead); err != ErrPolicyExists {
		t.Fatalf("want ErrPolicyExists, got %v", err)
	}
}

func TestInvalidRequest(t *testing.T) {
	eng := NewEngine()
	pub, audit := eng.Evaluate(context.Background(), Request{Subject: Attributes{"role": "admin"}})
	if pub.Allowed || audit.Reason != ReasonInvalidRequest {
		t.Fatalf("expected invalid request deny, got %+v %+v", pub, audit)
	}
}

func mustRegister(t *testing.T, eng *Engine, policies ...Policy) {
	t.Helper()
	for _, p := range policies {
		if err := eng.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.ID, err)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
