package mesh_test

import (
	"testing"

	"ontology/mesh"
)

func wv(sub string, n int) mesh.Target { return mesh.Target{Subset: sub, Weight: n} }

func validTargets() []mesh.Target { return []mesh.Target{wv("a", 100)} }

// TestValidationErrors covers weight/policy/shadow classes, their priority
// and smallest-rule-index reporting.
func TestValidationErrors(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()

	invalidArg := &mesh.Config{Rules: []mesh.Rule{{
		Matchers: nil,
		Targets:  validTargets(),
	}}}
	_, err := s.Publish(invalidArg, 0)
	expectErrKind(t, l, "Publish", "rule without matcher", err, mesh.KindInvalidArgument,
		"structural problem is InvalidArgument")

	badWeight := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{wv("a", 60), wv("b", 30)}},
	}}
	_, err = s.Publish(badWeight, 0)
	expectErrKind(t, l, "Publish", "weights sum 90", err, mesh.KindWeightInvalid,
		"weights must sum to 100")

	badRange := &mesh.Config{Rules: []mesh.Rule{{
		Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{wv("a", 101), wv("b", -1)},
	}}}
	_, err = s.Publish(badRange, 0)
	expectErrKind(t, l, "Publish", "weight 101 / -1", err, mesh.KindWeightInvalid,
		"weight range violations are WeightInvalid")

	emptyName := &mesh.Config{Rules: []mesh.Rule{{
		Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{{Subset: "", Weight: 100}},
	}}}
	_, err = s.Publish(emptyName, 0)
	expectErrKind(t, l, "Publish", "empty subset name", err, mesh.KindWeightInvalid,
		"empty subset name belongs to the weight class")

	indexOrder := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{wv("a", 99)}},
		{Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{wv("b", 98)}},
	}}
	_, err = s.Publish(indexOrder, 0)
	l.record("Publish", "two bad-weight rules", describeErr(err), "smallest rule index reported")
	if err == nil || err.Kind != mesh.KindWeightInvalid || err.RuleIndex != 0 {
		t.Fatalf("want WeightInvalid rule0, got %v", err)
	}

	badFallback := &mesh.Config{
		Rules:    []mesh.Rule{{Matchers: []mesh.Matcher{{}}, Targets: validTargets()}},
		Fallback: []mesh.Target{wv("fb", 70)},
	}
	_, err = s.Publish(badFallback, 0)
	l.record("Publish", "fallback sum 70", describeErr(err), "fallback uses FallbackRuleIndex")
	if err == nil || err.Kind != mesh.KindWeightInvalid || err.RuleIndex != mesh.FallbackRuleIndex {
		t.Fatalf("want WeightInvalid fallback, got %v", err)
	}

	conflicting := &mesh.Config{
		Default: mesh.Policy{Timeout: ip(10), PerAttemptTime: ip(20)},
		Rules: []mesh.Rule{
			{
				Matchers: []mesh.Matcher{{}},
				Targets:  []mesh.Target{wv("a", 90)},
			},
			{
				Matchers: []mesh.Matcher{{}},
				Targets:  validTargets(),
			},
		},
	}
	_, err = s.Publish(conflicting, 0)
	expectErrKind(t, l, "Publish", "weight+policy+shadow problems", err, mesh.KindWeightInvalid,
		"weight class outranks policy and shadow classes")

	conflicting2 := &mesh.Config{
		Rules: []mesh.Rule{
			{
				Matchers: []mesh.Matcher{{}},
				Targets:  validTargets(),
				Policy:   &mesh.Policy{Timeout: ip(10), PerAttemptTime: ip(20)},
			},
			{Matchers: []mesh.Matcher{{}}, Targets: validTargets()},
		},
	}
	_, err = s.Publish(conflicting2, 0)
	expectErrKind(t, l, "Publish", "policy+shadow problems", err, mesh.KindPolicyInvalid,
		"per-attempt > timeout; policy class outranks shadow")

	badProduct := &mesh.Config{
		Default: mesh.Policy{Timeout: ip(100), Retries: ip(6), PerAttemptTime: ip(20)},
		Rules:   []mesh.Rule{{Matchers: []mesh.Matcher{{}}, Targets: validTargets()}},
	}
	_, err = s.Publish(badProduct, 0)
	expectErrKind(t, l, "Publish", "6*20>100", err, mesh.KindPolicyInvalid,
		"retries * per-attempt-timeout must fit in timeout")

	structuralFirst := &mesh.Config{Rules: []mesh.Rule{{
		Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathExact, Value: "noslash"}}},
		Targets:  []mesh.Target{wv("a", 1)},
	}}}
	_, err = s.Publish(structuralFirst, 0)
	expectErrKind(t, l, "Publish", "bad path + bad weights", err, mesh.KindInvalidArgument,
		"invalid argument has highest priority")

	_, err = s.Route(mesh.Request{Path: "/", Bucket: 10000})
	expectErrKind(t, l, "Route", "bucket=10000", err, mesh.KindInvalidArgument,
		"bucket outside [0,9999] rejected")
}

// TestShadowImplications exercises the implication lattice and its
// non-implications.
func TestShadowImplications(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()

	cfg := &mesh.Config{Rules: []mesh.Rule{
		{
			Matchers: []mesh.Matcher{{
				Path:    &mesh.PathCondition{Kind: mesh.PathExact, Value: "/p"},
				Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderExact, Value: "v"}},
			}},
			Targets: validTargets(),
		},
		{
			Matchers: []mesh.Matcher{{
				Path:    &mesh.PathCondition{Kind: mesh.PathExact, Value: "/p"},
				Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderExists}},
			}},
			Targets: validTargets(),
		},
	}}
	_, err := s.Publish(cfg, 0)
	expectErrKind(t, l, "Publish", "exact header -> later exists", err, mesh.KindShadowedRule,
		"exact value entails exists; equal exact paths cover each other")

	cfg2 := &mesh.Config{Rules: []mesh.Rule{
		{
			Matchers: []mesh.Matcher{{
				Path:    &mesh.PathCondition{Kind: mesh.PathPrefix, Value: "/a"},
				Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderPrefix, Value: "abc"}},
			}},
			Targets: validTargets(),
		},
		{
			Matchers: []mesh.Matcher{{
				Path:    &mesh.PathCondition{Kind: mesh.PathExact, Value: "/a/b"},
				Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderPrefix, Value: "ab"}},
			}},
			Targets: validTargets(),
		},
	}}
	_, err = s.Publish(cfg2, 0)
	expectErrKind(t, l, "Publish", "prefix /a + header abc -> exact /a/b + ab",
		err, mesh.KindShadowedRule,
		"prefix covers deeper exact on segment boundary; longer prefix entails shorter")

	cfg3 := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{
			{Name: "h", Op: mesh.HeaderPrefix, Value: "xyz"},
		}}}, Targets: validTargets()},
		{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{
			{Name: "h", Op: mesh.HeaderPrefix, Value: "xyza"},
		}}}, Targets: validTargets()},
	}}
	v, err := s.Publish(cfg3, 0)
	l.record("Publish", "shorter header prefix vs longer",
		"v="+itoa2(v)+" "+describeErr(err), "later rule still reachable; accepted")
	if err != nil {
		t.Fatalf("should not be shadowed: %v", err)
	}

	cfg4 := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathPrefix, Value: "/a"}}},
			Targets: validTargets()},
		{Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathExact, Value: "/ab"}}},
			Targets: validTargets()},
	}}
	_, err = s.Publish(cfg4, v)
	l.record("Publish", "prefix /a vs exact /ab", describeErr(err),
		"half-segment is not coverage; accepted")
	if err != nil {
		t.Fatalf("/a must not cover /ab: %v", err)
	}

	cfg5 := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{
			{Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderExists}}},
			{Path: &mesh.PathCondition{Kind: mesh.PathExact, Value: "/z"}},
		}, Targets: validTargets()},
		{Matchers: []mesh.Matcher{
			{Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderExists}}},
		}, Targets: validTargets()},
	}}
	v5, err := s.Publish(cfg5, v+1)
	expectErrKind(t, l, "Publish", "rule1 matcher covered by rule0 matcher#0",
		err, mesh.KindShadowedRule, "every matcher covered by some earlier rule matcher")

	cfg6 := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{{Name: "a", Op: mesh.HeaderExists}}}},
			Targets: validTargets()},
		{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{{Name: "b", Op: mesh.HeaderExists}}}},
			Targets: validTargets()},
	}}
	_, err = s.Publish(cfg6, v5)
	l.record("Publish", "header a vs header b", describeErr(err), "different names never entail; accepted")
	if err != nil {
		t.Fatalf("different header names must not shadow: %v", err)
	}
}

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

// TestVersionConflict covers optimistic versioning, monotonic increment and
// failed publish leaving state intact.
func TestVersionConflict(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "a", "e1")
	base := mustPublish(t, l, s, &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{}}, Targets: validTargets()},
	}}, 0)

	_, err := s.Publish(&mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{}}, Targets: validTargets()},
	}}, 0)
	expectErrKind(t, l, "Publish", "base=0 but current=1", err, mesh.KindVersionConflict,
		"stale base rejected")

	_, err = s.Publish(&mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{wv("x", 10)}},
	}}, 1)
	l.record("Publish", "invalid config on correct base", describeErr(err),
		"validation failure must not change version/config")
	if err == nil || err.Kind != mesh.KindWeightInvalid {
		t.Fatalf("want WeightInvalid, got %v", err)
	}
	if s.CurrentVersion() != 1 {
		t.Fatalf("version changed after failed publish: %d", s.CurrentVersion())
	}
	expectRoute(t, l, s, mesh.Request{Path: "/", Bucket: 0}, "a", 0,
		"previous config still live after rejected publish")

	base2 := mustPublish(t, l, s, &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{}}, Targets: validTargets()},
	}}, base)
	if base2 != 2 {
		t.Fatalf("version = %d, want 2", base2)
	}
}

// TestImmutability verifies neither caller-input mutation after publish nor
// returned-config mutation can change live state.
func TestImmutability(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "a", "e1")
	cfg := &mesh.Config{Rules: []mesh.Rule{
		{Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathExact, Value: "/keep"}}},
			Targets: []mesh.Target{wv("a", 100)}},
	}}
	mustPublish(t, l, s, cfg, 0)

	cfg.Rules[0].Matchers[0].Path.Value = "/mutated"
	cfg.Rules[0].Targets[0].Weight = 99

	got := s.Config()
	got.Rules[0].Matchers[0].Path.Value = "/mutated2"
	got.Rules[0].Targets[0].Subset = "hacker"

	expectRoute(t, l, s, mesh.Request{Path: "/keep", Bucket: 0}, "a", 0,
		"live config unaffected by input or returned-config mutation")
}
