package mesh_test

import (
	"testing"

	"ontology/mesh"
)

func singleTarget(subset string) []mesh.Target {
	return []mesh.Target{{Subset: subset, Weight: 100}}
}

// TestSegmentBoundary covers exact vs prefix matching and the requirement
// that prefixes match only on whole segment boundaries.
func TestSegmentBoundary(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "a", "e1")
	cfg := &mesh.Config{
		Rules: []mesh.Rule{
			{
				Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathPrefix, Value: "/api"}}},
				Targets:  singleTarget("a"),
			},
		},
	}
	v := mustPublish(t, l, s, cfg, 0)

	cases := []struct {
		path string
		ok   bool
		why  string
	}{
		{"/api", true, "prefix equals the whole path"},
		{"/api/users", true, "prefix followed by segment boundary '/'"},
		{"/api/", true, "trailing slash is still a boundary"},
		{"/apix", false, "'/api' is half a segment of '/apix'"},
		{"/api123/x", false, "first segment '/api123' is not '/api'"},
		{"/ap", false, "shorter than prefix"},
		{"/", false, "root is not below '/api'"},
	}
	for _, c := range cases {
		r, err := s.Route(mesh.Request{Path: c.path, Bucket: 0})
		l.record("Route", c.path, fmtOK(r, err), c.why)
		if c.ok {
			if err != nil || r.Subset != "a" {
				t.Fatalf("path %s expected match, got %v %v", c.path, r, err)
			}
		} else if err == nil || err.Kind != mesh.KindNoRoute {
			t.Fatalf("path %s expected NoRoute, got %v %v", c.path, r, err)
		}
	}
	_ = v
}

func fmtOK(r *mesh.RouteResult, err *mesh.Error) string {
	return describeResult(r) + " err=" + describeErr(err)
}

// TestQueryString verifies the query is stripped before path matching.
func TestQueryString(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "a", "e1")
	cfg := &mesh.Config{
		Rules: []mesh.Rule{{
			Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathExact, Value: "/x"}}},
			Targets:  singleTarget("a"),
		}},
	}
	mustPublish(t, l, s, cfg, 0)
	expectRoute(t, l, s, mesh.Request{Path: "/x?a=1&b=2", Bucket: 0}, "a", 0,
		"query stripped; exact '/x' matches")
	expectRoute(t, l, s, mesh.Request{Path: "/x?", Bucket: 0}, "a", 0,
		"empty query also stripped")
}

// TestHeaderConditions covers exact/prefix/exists, case-insensitive names,
// case-sensitive values and multi-valued headers (any value satisfies).
func TestHeaderConditions(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "ex", "e1")
	regReady(s, "pf", "e2")
	regReady(s, "pr", "e3")
	cfg := &mesh.Config{
		Rules: []mesh.Rule{
			{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{
				{Name: "x-canary", Op: mesh.HeaderExact, Value: "yes"},
			}}}, Targets: singleTarget("ex")},
			{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{
				{Name: "X-Tenant", Op: mesh.HeaderPrefix, Value: "team-"},
			}}}, Targets: singleTarget("pf")},
			{Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{
				{Name: "trace", Op: mesh.HeaderExists},
			}}}, Targets: singleTarget("pr")},
		},
	}
	mustPublish(t, l, s, cfg, 0)

	expectRoute(t, l, s, mesh.Request{Path: "/", Headers: map[string][]string{"X-Canary": {"yes"}}, Bucket: 0}, "ex", 0,
		"name case-insensitive, exact value match")
	_, err := s.Route(mesh.Request{Path: "/", Headers: map[string][]string{"x-canary": {"Yes"}}, Bucket: 0})
	l.record("Route", "x-canary=Yes", describeErr(err), "values are case-sensitive -> falls through later rules -> NoRoute")
	if err == nil || err.Kind != mesh.KindNoRoute {
		t.Fatalf("case-sensitive exact expected NoRoute, got %v", err)
	}
	expectRoute(t, l, s, mesh.Request{Path: "/", Headers: map[string][]string{"x-tenant": {"team-alpha"}}, Bucket: 0}, "pf", 1,
		"prefix value match")
	expectRoute(t, l, s, mesh.Request{Path: "/", Headers: map[string][]string{"Trace": {""}}, Bucket: 0}, "pr", 2,
		"exists requires presence only, empty value is present")
	expectRoute(t, l, s, mesh.Request{
		Path:    "/",
		Headers: map[string][]string{"x-canary": {"no", "yes"}},
		Bucket:  0,
	}, "ex", 0, "multi-valued header: any value satisfies")
}

// TestFirstHitAndFallback verifies declaration order wins and later rules
// are never examined, plus fallback selection.
func TestFirstHitAndFallback(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "first", "e1")
	regReady(s, "second", "e2")
	regReady(s, "fb", "e3")
	cfg := &mesh.Config{
		Rules: []mesh.Rule{
			{Matchers: []mesh.Matcher{{
				Headers: []mesh.HeaderCondition{{Name: "h", Op: mesh.HeaderExists}},
			}}, Targets: singleTarget("first")},
			{Matchers: []mesh.Matcher{{
				Headers: []mesh.HeaderCondition{{Name: "h2", Op: mesh.HeaderExists}},
			}}, Targets: singleTarget("second")},
		},
		Fallback: singleTarget("fb"),
	}
	mustPublish(t, l, s, cfg, 0)
	expectRoute(t, l, s, mesh.Request{
		Path: "/anything", Headers: map[string][]string{"h": {"1"}, "h2": {"1"}}, Bucket: 0,
	}, "first", 0, "both rules match: declaration order, rule0 wins, rule1 never examined")

	s2 := mesh.NewService()
	regReady(s2, "second", "e2")
	regReady(s2, "fb", "e3")
	cfg2 := &mesh.Config{
		Rules: []mesh.Rule{{
			Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{{Name: "nope", Op: mesh.HeaderExists}}}},
			Targets:  singleTarget("second"),
		}},
		Fallback: singleTarget("fb"),
	}
	mustPublish(t, l, s2, cfg2, 0)
	expectRoute(t, l, s2, mesh.Request{Path: "/", Bucket: 0}, "fb", -1,
		"no rule matches -> fallback target")

	s3 := mesh.NewService()
	mustPublish(t, l, s3, &mesh.Config{
		Rules: []mesh.Rule{{
			Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{{Name: "nope", Op: mesh.HeaderExists}}}},
			Targets:  singleTarget("x"),
		}},
	}, 0)
	_, e := s3.Route(mesh.Request{Path: "/", Bucket: 0})
	expectErrKind(t, l, "Route", "no rule, no fallback", e, mesh.KindNoRoute,
		"no rule hit and no fallback target list")
}

// TestBucketBoundaries verifies consecutive bucket ownership and exact
// boundaries: weight w owns w*100 values in declaration order.
func TestBucketBoundaries(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "a", "e1")
	regReady(s, "b", "e2")
	regReady(s, "c", "e3")
	targets := []mesh.Target{{Subset: "a", Weight: 20}, {Subset: "b", Weight: 30}, {Subset: "c", Weight: 50}}
	mustPublish(t, l, s, &mesh.Config{
		Rules: []mesh.Rule{{Matchers: []mesh.Matcher{{}}, Targets: targets}},
	}, 0)
	bounds := []struct {
		bucket int
		subset string
		target int
	}{
		{0, "a", 0}, {1999, "a", 0}, // a owns [0,2000)
		{2000, "b", 1}, {4999, "b", 1}, // b owns [2000,5000)
		{5000, "c", 2}, {9999, "c", 2}, // c owns [5000,10000)
	}
	for _, b := range bounds {
		expectRoute(t, l, s, mesh.Request{Path: "/", Bucket: b.bucket}, b.subset, 0,
			"cumulative cut boundaries; both sides checked")
	}

	// Zero-weight target owns no buckets.
	s2 := mesh.NewService()
	regReady(s2, "a", "e1")
	regReady(s2, "zero", "e2")
	mustPublish(t, l, s2, &mesh.Config{
		Rules: []mesh.Rule{{Matchers: []mesh.Matcher{{}}, Targets: []mesh.Target{
			{Subset: "zero", Weight: 0}, {Subset: "a", Weight: 100},
		}}},
	}, 0)
	for _, b := range []int{0, 5000, 9999} {
		expectRoute(t, l, s2, mesh.Request{Path: "/", Bucket: b}, "a", 0,
			"weight-zero target gets no bucket")
	}
}

// TestPolicyInheritance verifies per-field override/default/unset
// resolution, including the fallback using the service default.
func TestPolicyInheritance(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "ov", "e1")
	regReady(s, "fb", "e2")
	cfg := &mesh.Config{
		Default: mesh.Policy{Timeout: ip(1000), Retries: ip(3)},
		Rules: []mesh.Rule{{
			Matchers: []mesh.Matcher{{Headers: []mesh.HeaderCondition{{Name: "x", Op: mesh.HeaderExists}}}},
			Targets:  singleTarget("ov"),
			Policy:   &mesh.Policy{Timeout: ip(500), PerAttemptTime: ip(100)},
		}},
		Fallback: singleTarget("fb"),
	}
	mustPublish(t, l, s, cfg, 0)

	r, err := s.Route(mesh.Request{Path: "/", Headers: map[string][]string{"x": {"1"}}, Bucket: 0})
	l.record("Route", "header x present", describeResult(r), "timeout overridden, retries inherited, perAttempt from rule")
	if err != nil || *r.Policy.Timeout != 500 || *r.Policy.Retries != 3 || *r.Policy.PerAttemptTime != 100 {
		t.Fatalf("bad merged policy: %s err=%v", describeResult(r), err)
	}
	r2, err := s.Route(mesh.Request{Path: "/", Bucket: 0})
	l.record("Route", "fallback", describeResult(r2), "fallback uses pure service default; perAttempt unset")
	if err != nil || *r2.Policy.Timeout != 1000 || *r2.Policy.Retries != 3 || r2.Policy.PerAttemptTime != nil {
		t.Fatalf("bad fallback policy: %s err=%v", describeResult(r2), err)
	}
}

// TestNoEndpoint covers unregistered subset, no-ready-endpoints, no
// rerouting and no state mutation on failure.
func TestNoEndpoint(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "good", "e1")
	if err := s.Registry.Register(mesh.SubsetDef{Name: "down", Endpoints: []mesh.Endpoint{
		{Addr: "d1", Ready: false}, {Addr: "d2", Ready: false},
	}}); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, l, s, &mesh.Config{Rules: []mesh.Rule{{
		Matchers: []mesh.Matcher{{}},
		Targets: []mesh.Target{
			{Subset: "missing", Weight: 50}, {Subset: "good", Weight: 50},
		},
	}}}, 0)

	_, err := s.Route(mesh.Request{Path: "/", Bucket: 0})
	expectErrKind(t, l, "Route", "bucket=0 -> subset 'missing' unregistered", err, mesh.KindNoEndpoint,
		"selected subset unregistered; no reroute to 'good'")
	if err.RuleIndex != 0 {
		t.Fatalf("error rule index = %d", err.RuleIndex)
	}
	// Endpoint becomes ready after readiness change; config/version untouched.
	if e := s.Registry.SetReady("down", "d1", true); e != nil {
		t.Fatal(e)
	}
	if s.CurrentVersion() != 1 {
		t.Fatalf("version changed unexpectedly: %d", s.CurrentVersion())
	}

	expectRoute(t, l, s, mesh.Request{Path: "/", Bucket: 6000}, "good", 0,
		"sanity: the other target works, proving no state corruption")
}
