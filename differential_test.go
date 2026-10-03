package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/rewrite"
	"ontology/route"
)

type modelConfig struct {
	routes  []route.Route
	rules   []rewrite.Rule
	version uint64
}

type modelResult struct {
	ok      bool
	kind    string
	stage   string
	backend string
	final   string
	hops    int
	chain   []string
	version uint64
	audit   uint64
}

type model struct {
	config modelConfig
	audit  uint64
}

func uniquePrefixes(rng *rand.Rand, count int, reserved []string) []string {
	prefixes := append([]string(nil), reserved...)
	seen := map[string]bool{}
	for _, prefix := range prefixes {
		seen[prefix] = true
	}
	words := []string{"pub", "api", "int", "a", "b", "new", "old", "x", "y", "z"}
	for len(prefixes) < count {
		prefix := "/" + words[rng.Intn(len(words))]
		if rng.Intn(3) == 0 {
			prefix += "/" + words[rng.Intn(len(words))]
		}
		if !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

func randomRoutes(rng *rand.Rand) ([]route.Route, error) {
	prefixes := uniquePrefixes(rng, rng.Intn(8), []string{"/pub", "/api", "/int", "/a", "/b", "/new", "/old", "/x"})
	routes := make([]route.Route, 0, len(prefixes))
	for _, prefix := range prefixes {
		scope := ""
		switch rng.Intn(3) {
		case 1:
			scope = "api"
		case 2:
			scope = "admin"
		}
		routes = append(routes, route.Route{Prefix: prefix, Scope: scope, Backend: "b-" + prefix[1:]})
	}
	if rng.Intn(20) == 0 {
		routes = append(routes, route.Route{Prefix: routes[0].Prefix, Scope: "", Backend: "dup"})
	}
	if _, err := route.NewTable(routes); err != nil {
		return routes, err
	}
	return routes, nil
}

func randomRules(rng *rand.Rand, routes []route.Route) ([]rewrite.Rule, error) {
	prefixes := uniquePrefixes(rng, rng.Intn(8), []string{"/a", "/b", "/old", "/new"})
	rules := make([]rewrite.Rule, 0, len(prefixes))
	for _, from := range prefixes {
		to := routes[rng.Intn(len(routes))].Prefix
		if rng.Intn(5) == 0 {
			to = "/"
		}
		rules = append(rules, rewrite.Rule{From: from, To: to, Final: rng.Intn(4) == 0})
	}
	if rng.Intn(20) == 0 && len(rules) > 0 {
		rules = append(rules, rewrite.Rule{From: rules[0].From, To: "/x"})
	}
	if _, err := rewrite.NewRuleSet(rules); err != nil {
		return rules, err
	}
	return rules, nil
}

func modelLookup(cfg modelConfig, path string) (route.Route, string, bool) {
	var best route.Route
	bestLen := -1
	for _, item := range cfg.routes {
		if path == item.Prefix || strings.HasPrefix(path, item.Prefix+"/") || item.Prefix == "/" {
			if len(item.Prefix) > bestLen || item.Prefix == "/" && bestLen == -1 {
				best = item
				bestLen = len(item.Prefix)
			}
		}
	}
	if bestLen < 0 {
		return route.Route{}, "", false
	}
	rest := ""
	if best.Prefix == "/" {
		rest = path
		if path == "/" {
			rest = ""
		}
	} else if len(path) > len(best.Prefix) {
		rest = path[len(best.Prefix):]
	}
	return best, rest, true
}

func modelRule(cfg modelConfig, path string) (rewrite.Rule, string, bool) {
	var best rewrite.Rule
	found := false
	bestLen := -1
	for _, item := range cfg.rules {
		if path == item.From || strings.HasPrefix(path, item.From+"/") || item.From == "/" {
			if !found || len(item.From) > bestLen || item.From == "/" && bestLen < 1 {
				best = item
				bestLen = len(item.From)
				found = true
			}
		}
	}
	if !found {
		return rewrite.Rule{}, "", false
	}
	rest := ""
	if best.From == "/" {
		rest = path
		if path == "/" {
			rest = ""
		}
	} else if len(path) > len(best.From) {
		rest = path[len(best.From):]
	}
	return best, rest, true
}

func modelAllowed(required string, scopes []string) bool {
	if required == "" {
		return true
	}
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

func (m *model) handle(k int, rawPath string, scopes []string) modelResult {
	for _, scope := range scopes {
		if scope == "" {
			return modelResult{kind: "invalid_argument", stage: StageArgument}
		}
	}
	p0, err := route.Normalize(rawPath)
	if err != nil {
		return modelResult{kind: "invalid_path", stage: StagePath}
	}
	cfg := m.config
	original, _, routed := modelLookup(cfg, p0)
	if routed && !modelAllowed(original.Scope, scopes) {
		return modelResult{kind: "unauthorized", stage: StageOriginal, final: p0, chain: []string{p0}, version: cfg.version}
	}
	cur := p0
	seen := map[string]bool{p0: true}
	chain := []string{p0}
	hops := 0
	for {
		rule, rest, ok := modelRule(cfg, cur)
		if !ok {
			break
		}
		if hops == k {
			return modelResult{kind: "hop_limit", stage: StageRewrite, final: cur, hops: hops, chain: append([]string(nil), chain...), version: cfg.version}
		}
		target := rule.To + rest
		if rule.To == "/" && rest != "" {
			target = rest
		}
		next, err := route.Normalize(target)
		if err != nil {
			return modelResult{kind: "invalid_path", stage: StageRewrite, final: target, hops: hops, chain: append([]string(nil), chain...), version: cfg.version}
		}
		if seen[next] {
			return modelResult{kind: "loop", stage: StageRewrite, final: next, hops: hops, chain: append(append([]string(nil), chain...), next), version: cfg.version}
		}
		seen[next] = true
		hops++
		cur = next
		chain = append(chain, next)
		if rule.Final {
			break
		}
	}
	final, _, ok := modelLookup(cfg, cur)
	if !ok {
		return modelResult{kind: "no_route", stage: StageFinal, final: cur, hops: hops, chain: append([]string(nil), chain...), version: cfg.version}
	}
	if !modelAllowed(final.Scope, scopes) {
		return modelResult{kind: "unauthorized", stage: StageFinal, final: cur, hops: hops, chain: append([]string(nil), chain...), version: cfg.version}
	}
	m.audit++
	return modelResult{ok: true, backend: final.Backend, final: cur, hops: hops, chain: append([]string(nil), chain...), version: cfg.version, audit: m.audit}
}

func randomPath(rng *rand.Rand) string {
	words := []string{"pub", "api", "int", "a", "b", "new", "old", "x", "legacy", "old", "k", "..", ".", ""}
	parts := make([]string, 0, 4)
	for i := 0; i < rng.Intn(4); i++ {
		parts = append(parts, words[rng.Intn(len(words))])
	}
	return "/" + strings.Join(parts, "/") + "/"
}

func compareResult(t *testing.T, seed, index int64, input string, want modelResult, got Result, err error) {
	t.Helper()
	basis := fmt.Sprintf("seed=%d op=%d input=%s", seed, index, input)
	if want.ok {
		if err != nil {
			t.Fatalf("%s unexpected error=%v", basis, err)
		}
		if got.Backend != want.backend || got.FinalPath != want.final || got.Hops != want.hops || got.Version != want.version || got.Audit != want.audit || strings.Join(got.Chain, ",") != strings.Join(want.chain, ",") {
			t.Fatalf("%s got=%+v want=%+v", basis, got, want)
		}
		t.Logf("%s => success backend=%s final=%s hops=%d chain=%v version=%d audit=%d basis=route/rewrite/authz", input, got.Backend, got.FinalPath, got.Hops, got.Chain, got.Version, got.Audit)
		return
	}
	var gatewayErr *Error
	if err == nil || !AsError(err, &gatewayErr) {
		t.Fatalf("%s expected error got=%+v", basis, got)
	}
	if gatewayErr.Kind != want.kind || gatewayErr.Stage != want.stage || gatewayErr.Version != want.version || strings.Join(gatewayErr.Chain, ",") != strings.Join(want.chain, ",") {
		t.Fatalf("%s got=(%s/%s chain=%v version=%d) want=(%s/%s chain=%v version=%d)", basis, gatewayErr.Kind, gatewayErr.Stage, gatewayErr.Chain, gatewayErr.Version, want.kind, want.stage, want.chain, want.version)
	}
	t.Logf("%s => failure kind=%s stage=%s chain=%v version=%d basis=first-failing-spec-stage", input, want.kind, want.stage, want.chain, want.version)
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		k := rng.Intn(6)
		router, err := NewRouter(k)
		if err != nil {
			t.Fatal(err)
		}
		m := &model{}
		routes := baseRoutes()
		rules := baseRules()
		if err := router.SetRoutes(routes); err == nil {
			m.config.routes = append([]route.Route(nil), routes...)
			m.config.version = 1
		}
		if err := router.SetRules(rules); err == nil {
			m.config.rules = append([]rewrite.Rule(nil), rules...)
			m.config.version = 2
		}

		for op := 0; op < 12; op++ {
			switch rng.Intn(10) {
			case 0:
				routes, err = randomRoutes(rng)
				t.Logf("seed=%d op=%d input=SetRoutes routes=%v => err=%v basis=atomic-validation-and-version", seed, op, routes, err)
				setErr := router.SetRoutes(routes)
				if (err == nil) != (setErr == nil) {
					t.Fatalf("route validation mismatch model=%v router=%v", err, setErr)
				}
				if setErr == nil {
					m.config.routes = append([]route.Route(nil), routes...)
					m.config.version++
				} else {
					routes = m.config.routes
				}
			case 1:
				rules, err = randomRules(rng, m.config.routes)
				setErr := router.SetRules(rules)
				t.Logf("seed=%d op=%d input=SetRules rules=%v => model_err=%v router_err=%v basis=atomic-validation-and-version", seed, op, rules, err, setErr)
				if (err == nil) != (setErr == nil) {
					t.Fatalf("rule validation mismatch model=%v router=%v", err, setErr)
				}
				if setErr == nil {
					m.config.rules = append([]rewrite.Rule(nil), rules...)
					m.config.version++
				} else {
					rules = m.config.rules
				}
			default:
				path := randomPath(rng)
				scopes := []string{}
				if rng.Intn(2) == 0 {
					scopes = append(scopes, "api")
				}
				if rng.Intn(2) == 0 {
					scopes = append(scopes, "admin")
				}
				if rng.Intn(40) == 0 {
					scopes = append(scopes, "")
				}
				want := m.handle(k, path, scopes)
				got, handleErr := router.Handle(path, scopes)
				compareResult(t, seed, int64(op), fmt.Sprintf("Handle(%q,%v)", path, scopes), want, got, handleErr)
			}
		}
	}
}
