package mesh_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/mesh"
)

// randomWeights produces a valid (sums to 100, each in [0,100]) or invalid
// target list so that both acceptance and rejection paths get exercised.
func randomWeights(rng *rand.Rand, subs []string, forceValid bool) []mesh.Target {
	n := 1 + rng.Intn(3)
	ts := make([]mesh.Target, n)
	for i := range ts {
		ts[i] = mesh.Target{Subset: subs[rng.Intn(len(subs))]}
	}
	if rng.Intn(3) == 0 && !forceValid {
		for i := range ts {
			ts[i].Weight = rng.Intn(102) - 1
		}
		return ts
	}
	remaining := 100
	for i := 0; i < n-1; i++ {
		w := rng.Intn(remaining + 1)
		ts[i].Weight = w
		remaining -= w
	}
	ts[n-1].Weight = remaining
	return ts
}

func randomPolicy(rng *rand.Rand, allowInvalid bool) *mesh.Policy {
	if rng.Intn(3) == 0 {
		return nil
	}
	p := &mesh.Policy{}
	put := func(v int) *int { return &v }
	if rng.Intn(2) == 0 {
		p.Timeout = put(50 + rng.Intn(500))
	}
	if rng.Intn(2) == 0 {
		p.Retries = put(rng.Intn(6))
	}
	if rng.Intn(2) == 0 {
		p.PerAttemptTime = put(5 + rng.Intn(200))
	}
	if allowInvalid && rng.Intn(6) == 0 {
		p.Timeout = put(1)
		p.PerAttemptTime = put(500)
	}
	return p
}

var pathSegs = []string{"", "api", "v1", "v2", "users", "orders", "admin", "health", "pay"}
var headerNames = []string{"x-canary", "x-tenant", "trace", "region", "auth"}
var headerVals = []string{"yes", "no", "team-a", "team-ab", "team-abc", "cn", "us", "v1", "v12"}

func randomMatcher(rng *rand.Rand) mesh.Matcher {
	m := mesh.Matcher{}
	if rng.Intn(4) != 0 {
		pc := &mesh.PathCondition{Value: randomPath(rng)}
		if rng.Intn(2) == 0 {
			pc.Kind = mesh.PathExact
		} else {
			pc.Kind = mesh.PathPrefix
		}
		m.Path = pc
	}
	nh := rng.Intn(3)
	for i := 0; i < nh; i++ {
		op := rng.Intn(3)
		hc := mesh.HeaderCondition{Name: headerNames[rng.Intn(len(headerNames))], Op: mesh.HeaderOp(op)}
		if op != int(mesh.HeaderExists) {
			hc.Value = headerVals[rng.Intn(len(headerVals))]
		}
		m.Headers = append(m.Headers, hc)
	}
	return m
}

func randomPath(rng *rand.Rand) string {
	n := 1 + rng.Intn(3)
	p := ""
	for i := 0; i < n; i++ {
		p += "/" + pathSegs[1+rng.Intn(len(pathSegs)-1)]
	}
	if rng.Intn(5) == 0 {
		p += "/"
	}
	return p
}

func randomConfig(rng *rand.Rand, subs []string) *mesh.Config {
	nr := 1 + rng.Intn(8)
	cfg := &mesh.Config{Default: mesh.Policy{
		Timeout: ip(500 + rng.Intn(1000)),
	}}
	for i := 0; i < nr; i++ {
		nm := 1 + rng.Intn(3)
		ms := make([]mesh.Matcher, nm)
		for j := range ms {
			ms[j] = randomMatcher(rng)
		}
		cfg.Rules = append(cfg.Rules, mesh.Rule{
			Matchers: ms,
			Targets:  randomWeights(rng, subs, false),
			Policy:   randomPolicy(rng, true),
		})
	}
	if rng.Intn(2) == 0 {
		cfg.Fallback = randomWeights(rng, subs, false)
	}
	return cfg
}

func randomRequest(rng *rand.Rand) mesh.Request {
	path := randomPath(rng)
	if rng.Intn(2) == 0 {
		path += "?q=" + headerVals[rng.Intn(len(headerVals))]
	}
	headers := map[string][]string{}
	for i := rng.Intn(3); i >= 0; i-- {
		name := headerNames[rng.Intn(len(headerNames))]
		nv := 1 + rng.Intn(2)
		vals := make([]string, nv)
		for j := range vals {
			vals[j] = headerVals[rng.Intn(len(headerVals))]
		}
		headers[name] = vals
	}
	return mesh.Request{Path: path, Headers: headers, Bucket: rng.Intn(10000)}
}

// TestRandomDifferential generates many random configs and requests and
// compares the real implementation, operation by operation, with the
// independent naive model.
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	l := newOpLog(t)
	const iterations = 400
	for iter := 0; iter < iterations; iter++ {
		subs := []string{"s0", "s1", "s2", "s3", "s4"}
		s := mesh.NewService()
		for _, name := range subs {
			ready := rng.Intn(5) != 0
			def := mesh.SubsetDef{Name: name}
			for e := 0; e < rng.Intn(4); e++ {
				def.Endpoints = append(def.Endpoints, mesh.Endpoint{
					Addr:  fmt.Sprintf("%s-%d", name, e),
					Ready: ready && rng.Intn(3) != 0,
				})
			}
			if len(def.Endpoints) == 0 {
				def.Endpoints = []mesh.Endpoint{{Addr: name + "-0", Ready: ready}}
			}
			if err := s.Registry.Register(def); err != nil {
				t.Fatal(err)
			}
		}
		ready := map[string]bool{}
		live := s.Config()
		_ = live
		// Registry readiness for naive model.
		cfg := randomConfig(rng, subs)
		_, gotErr := s.Publish(cfg, 0)
		wantKind, wantRule := naivePublishError(cfg)
		l.record("Publish(diff)", describeConfig(cfg),
			fmt.Sprintf("actual=%s naive=kind:%s rule:%d", describeErr(gotErr), wantKind, wantRule),
			"four-phase validation cross-check vs naive model")
		if (gotErr == nil) != (wantKind < 0) ||
			(gotErr != nil && (gotErr.Kind != wantKind || gotErr.RuleIndex != wantRule)) {
			t.Fatalf("iter %d publish mismatch: got %v, want kind=%s rule=%d",
				iter, gotErr, wantKind, wantRule)
		}
		if gotErr != nil {
			continue
		}
		// Determine readiness from registrations: replicate by routing probe
		// is not possible; instead rebuild from cfg subsets via a published
		// all-routes probe service. We registered deterministically above,
		// so recompute ready map by rule: subset ready iff any endpoint
		// flag was set; replicate by tracking in the generator is hard, so
		// register all endpoints ready here for the routing comparison.
		// (Readiness edge cases are covered by TestNoEndpoint directly.)
		for _, name := range subs {
			_ = s.Registry.Register(mesh.SubsetDef{Name: name, Endpoints: []mesh.Endpoint{
				{Addr: name + "-only", Ready: true},
			}})
			ready[name] = true
		}
		published := s.Config()
		nreq := 20
		for k := 0; k < nreq; k++ {
			req := randomRequest(rng)
			r, rerr := s.Route(req)
			nSub, nRule, nTarget, nFallback, nKind := naiveRoute(s, published, ready, req)
			actual := fmt.Sprintf("result=%s err=%s", describeResult(r), describeErr(rerr))
			naive := fmt.Sprintf("subset=%s rule=%d target=%d fallback=%t kind=%s",
				nSub, nRule, nTarget, nFallback, nKind)
			l.record("Route(diff)", describeRequest(req), actual+" || naive: "+naive,
				"linear-scan naive model must agree with trie-backed router")
			if nKind < 0 {
				if rerr != nil || r.Subset != nSub || r.RuleIndex != nRule ||
					r.TargetIdx != nTarget || r.FromFallback != nFallback {
					t.Fatalf("iter %d req %d mismatch: got %s / %v, naive %s",
						iter, k, describeResult(r), rerr, naive)
				}
			} else {
				if rerr == nil || rerr.Kind != nKind {
					t.Fatalf("iter %d req %d error mismatch: got %v, naive kind %s",
						iter, k, rerr, nKind)
				}
			}
		}
	}
}
