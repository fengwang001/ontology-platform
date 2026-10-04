package gateway_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/gateway"
	"ontology/rewrite"
	"ontology/route"
)

// ---------- helpers ----------

func mustRouter(t *testing.T, k int, routes []route.Entry, rules []rewrite.Rule) *gateway.Router {
	t.Helper()
	r, err := gateway.NewRouter(k)
	if err != nil {
		t.Fatalf("NewRouter(%d): %v", k, err)
	}
	if err := r.SetRoutes(routes); err != nil {
		t.Fatalf("SetRoutes: %v", err)
	}
	if err := r.SetRules(rules); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	return r
}

type wantErr struct {
	kind  gateway.Kind
	stage gateway.Stage
}

func checkErr(t *testing.T, err error, want wantErr) {
	t.Helper()
	ge, ok := err.(*gateway.Error)
	if !ok {
		t.Fatalf("err = %v, want *gateway.Error", err)
	}
	if ge.Kind != want.kind {
		t.Fatalf("err kind = %v, want %v (err=%v)", ge.Kind, want.kind, ge)
	}
	if want.kind == gateway.KindDenied && ge.Stage != want.stage {
		t.Fatalf("denied stage = %v, want %v", ge.Stage, want.stage)
	}
}

type hcase struct {
	name   string
	path   string
	scopes []string
	want   *gateway.Result // nil => expect error
	werr   *wantErr
}

// runCases executes cases in order; expected audit numbers are assigned
// to successes in order (1, 2, ...), matching the success-only counter.
func runCases(t *testing.T, r *gateway.Router, version uint64, cases []hcase) {
	t.Helper()
	var audit uint64
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := r.Handle(c.path, c.scopes)
			if c.werr != nil {
				checkErr(t, err, *c.werr)
				return
			}
			if err != nil {
				t.Fatalf("Handle(%q, %v) error: %v", c.path, c.scopes, err)
			}
			audit++
			want := *c.want
			want.Audit = audit
			want.Version = version
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Handle(%q, %v) =\n  %+v\nwant\n  %+v", c.path, c.scopes, got, want)
			}
		})
	}
}

// ---------- spec examples ----------

var specRoutes = []route.Entry{
	{Prefix: "/pub", Scope: "", Backend: "pub"},
	{Prefix: "/api", Scope: "api", Backend: "api"},
	{Prefix: "/int", Scope: "admin", Backend: "int"},
}

var specRules = []rewrite.Rule{
	{From: "/pub/old", To: "/int/x"},
	{From: "/api/legacy", To: "/pub/n"},
	{From: "/a", To: "/b"},
	{From: "/b", To: "/a"},
}

func TestSpecMainK3(t *testing.T) {
	r := mustRouter(t, 3, specRoutes, specRules)
	runCases(t, r, 2, []hcase{
		{
			name:   "public origin cannot escape into admin backend",
			path:   "/pub/old/y",
			scopes: nil,
			werr:   &wantErr{gateway.KindDenied, gateway.StageFinal},
		},
		{
			name:   "admin scope reaches int backend after rewrite",
			path:   "/pub/old/y",
			scopes: []string{"admin"},
			want: &gateway.Result{
				P0: "/pub/old/y", Final: "/int/x/y", Backend: "int",
				Hops: 1, Chain: []string{"/pub/old/y", "/int/x/y"},
			},
		},
		{
			name:   "origin authz precedes rewrite even to public final",
			path:   "/api/legacy/z",
			scopes: nil,
			werr:   &wantErr{gateway.KindDenied, gateway.StageOrigin},
		},
		{
			name:   "api scope rewrites into public backend",
			path:   "/api/legacy/z",
			scopes: []string{"api"},
			want: &gateway.Result{
				P0: "/api/legacy/z", Final: "/pub/n/z", Backend: "pub",
				Hops: 1, Chain: []string{"/api/legacy/z", "/pub/n/z"},
			},
		},
		{
			name:   "messy path normalized first",
			path:   "/pub/../api//legacy/./z/",
			scopes: []string{"api"},
			want: &gateway.Result{
				P0: "/api/legacy/z", Final: "/pub/n/z", Backend: "pub",
				Hops: 1, Chain: []string{"/api/legacy/z", "/pub/n/z"},
			},
		},
		{
			name:   "segment boundary: /pub does not match /pubx",
			path:   "/pubx",
			scopes: nil,
			werr:   &wantErr{gateway.KindNoRoute, gateway.StageNone},
		},
		{
			name:   "loop a->b->a",
			path:   "/a",
			scopes: nil,
			werr:   &wantErr{gateway.KindLoop, gateway.StageNone},
		},
		{
			name:   "direct protected path denied at origin",
			path:   "/int/x",
			scopes: nil,
			werr:   &wantErr{gateway.KindDenied, gateway.StageOrigin},
		},
		{
			name:   "public passthrough without rules",
			path:   "/pub/g",
			scopes: nil,
			want: &gateway.Result{
				P0: "/pub/g", Final: "/pub/g", Backend: "pub",
				Hops: 0, Chain: []string{"/pub/g"},
			},
		},
	})
}

func TestSpecHopLimitAndLoop(t *testing.T) {
	t.Run("K=1 hop limit precedes loop", func(t *testing.T) {
		r := mustRouter(t, 1, specRoutes, specRules)
		_, err := r.Handle("/a", nil)
		checkErr(t, err, wantErr{gateway.KindHopLimit, gateway.StageNone})
	})
	t.Run("K=5 loop found", func(t *testing.T) {
		r := mustRouter(t, 5, specRoutes, specRules)
		_, err := r.Handle("/a", nil)
		checkErr(t, err, wantErr{gateway.KindLoop, gateway.StageNone})
	})
}

func TestOriginDenialPrecedesRewriteError(t *testing.T) {
	// The rewrite of /api/x loops onto itself, but a caller without the
	// api scope must see the origin denial, never the loop error.
	r := mustRouter(t, 3,
		[]route.Entry{{Prefix: "/api", Scope: "api", Backend: "api"}},
		[]rewrite.Rule{{From: "/api/x", To: "/api/x"}},
	)
	_, err := r.Handle("/api/x", nil)
	checkErr(t, err, wantErr{gateway.KindDenied, gateway.StageOrigin})
	_, err = r.Handle("/api/x", []string{"api"})
	checkErr(t, err, wantErr{gateway.KindLoop, gateway.StageNone})
}

func TestSpecFinal(t *testing.T) {
	routes := append(append([]route.Entry{}, specRoutes...),
		route.Entry{Prefix: "/new", Scope: "", Backend: "new"},
		route.Entry{Prefix: "/x", Scope: "", Backend: "x"},
	)
	rules := []rewrite.Rule{
		{From: "/old", To: "/new", Final: true},
		{From: "/new", To: "/x"},
	}
	t.Run("K=3 final stops chain", func(t *testing.T) {
		r := mustRouter(t, 3, routes, rules)
		runCases(t, r, 2, []hcase{
			{
				name: "final rule stops after one hop",
				path: "/old/k",
				want: &gateway.Result{
					P0: "/old/k", Final: "/new/k", Backend: "new",
					Hops: 1, Chain: []string{"/old/k", "/new/k"},
				},
			},
			{
				name: "non-final entry point continues",
				path: "/new/k",
				want: &gateway.Result{
					P0: "/new/k", Final: "/x/k", Backend: "x",
					Hops: 1, Chain: []string{"/new/k", "/x/k"},
				},
			},
		})
	})
	t.Run("K=0 matching rule exceeds limit regardless of final", func(t *testing.T) {
		r := mustRouter(t, 0, routes, rules)
		_, err := r.Handle("/old/k", nil)
		checkErr(t, err, wantErr{gateway.KindHopLimit, gateway.StageNone})
		_, err = r.Handle("/new/k", nil)
		checkErr(t, err, wantErr{gateway.KindHopLimit, gateway.StageNone})
		got, err := r.Handle("/x/k", nil)
		if err != nil {
			t.Fatalf("Handle(/x/k): %v", err)
		}
		if got.Backend != "x" || got.Hops != 0 {
			t.Fatalf("Handle(/x/k) = %+v", got)
		}
	})
}

// ---------- semantics ----------

func TestBadScopesCheckedFirst(t *testing.T) {
	r := mustRouter(t, 3, specRoutes, specRules)
	// Invalid scopes win over an invalid path.
	_, err := r.Handle("/%", []string{""})
	checkErr(t, err, wantErr{gateway.KindBadArgument, gateway.StageNone})
	_, err = r.Handle("/pub", []string{"admin", ""})
	checkErr(t, err, wantErr{gateway.KindBadArgument, gateway.StageNone})
	// The failures above must not consume audit numbers.
	got, err := r.Handle("/pub", nil)
	if err != nil {
		t.Fatalf("Handle(/pub): %v", err)
	}
	if got.Audit != 1 {
		t.Fatalf("audit = %d, want 1 (failures must not consume numbers)", got.Audit)
	}
}

func TestNewRouterKRange(t *testing.T) {
	for _, k := range []int{-1, 33, 100} {
		if _, err := gateway.NewRouter(k); err == nil {
			t.Fatalf("NewRouter(%d) succeeded, want error", k)
		} else if ge := err.(*gateway.Error); ge.Kind != gateway.KindBadArgument {
			t.Fatalf("NewRouter(%d) kind = %v, want bad-argument", k, ge.Kind)
		}
	}
	for _, k := range []int{0, 1, 32} {
		if _, err := gateway.NewRouter(k); err != nil {
			t.Fatalf("NewRouter(%d): %v", k, err)
		}
	}
}

func TestVersionAndAuditSemantics(t *testing.T) {
	r, err := gateway.NewRouter(3)
	if err != nil {
		t.Fatal(err)
	}
	if v := r.Version(); v != 0 {
		t.Fatalf("initial version = %d, want 0", v)
	}
	if err := r.SetRoutes(specRoutes); err != nil {
		t.Fatal(err)
	}
	if v := r.Version(); v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	// Rejected SetRoutes: duplicate prefix, version unchanged.
	if err := r.SetRoutes([]route.Entry{
		{Prefix: "/dup", Backend: "a"},
		{Prefix: "/dup", Backend: "b"},
	}); err == nil {
		t.Fatal("duplicate SetRoutes succeeded")
	} else if ge := err.(*gateway.Error); ge.Kind != gateway.KindBadArgument {
		t.Fatalf("kind = %v, want bad-argument", ge.Kind)
	}
	if v := r.Version(); v != 1 {
		t.Fatalf("version = %d after rejected SetRoutes, want 1", v)
	}
	if err := r.SetRules(specRules); err != nil {
		t.Fatal(err)
	}
	if v := r.Version(); v != 2 {
		t.Fatalf("version = %d, want 2 (shared counter)", v)
	}
	// Rejected SetRules: from not normalized, version unchanged.
	if err := r.SetRules([]rewrite.Rule{{From: "/trail/", To: "/x"}}); err == nil {
		t.Fatal("invalid SetRules succeeded")
	}
	if v := r.Version(); v != 2 {
		t.Fatalf("version = %d after rejected SetRules, want 2", v)
	}
	// Successes take audit 1, 2, 3; failures in between consume nothing.
	for i, want := range []uint64{1, 2, 3} {
		got, err := r.Handle("/pub", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Audit != want || got.Version != 2 {
			t.Fatalf("success %d: audit=%d version=%d, want audit=%d version=2",
				i, got.Audit, got.Version, want)
		}
		if i == 0 {
			if _, err := r.Handle("/pubx", nil); err == nil {
				t.Fatal("Handle(/pubx) succeeded")
			}
		}
		if i == 1 {
			if _, err := r.Handle("/a", nil); err == nil {
				t.Fatal("Handle(/a) succeeded, want loop")
			}
		}
	}
}

func TestAtomicReplaceOnFailure(t *testing.T) {
	r := mustRouter(t, 3, specRoutes, specRules)
	// Failed SetRoutes keeps the previous table.
	if err := r.SetRoutes([]route.Entry{
		{Prefix: "/only", Backend: "x"},
		{Prefix: "/bad//prefix", Backend: "y"},
	}); err == nil {
		t.Fatal("invalid SetRoutes succeeded")
	}
	// Failed SetRules keeps the previous rule set.
	if err := r.SetRules([]rewrite.Rule{
		{From: "/q", To: "/q"},
		{From: "/q", To: "/z"},
	}); err == nil {
		t.Fatal("invalid SetRules succeeded")
	}
	if v := r.Version(); v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	got, err := r.Handle("/pub/old/y", []string{"admin"})
	if err != nil {
		t.Fatalf("old config not in effect: %v", err)
	}
	if got.Backend != "int" || got.Final != "/int/x/y" || got.Version != 2 {
		t.Fatalf("got %+v, want old config result", got)
	}
	// A valid SetRoutes replaces wholesale and bumps the shared version.
	if err := r.SetRoutes([]route.Entry{{Prefix: "/", Backend: "root"}}); err != nil {
		t.Fatal(err)
	}
	got, err = r.Handle("/pub/old/y", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != "root" || got.Version != 3 {
		t.Fatalf("got %+v, want backend=root version=3", got)
	}
}

// ---------- concurrency ----------

func checkResultInvariants(t *testing.T, r *gateway.Router, res gateway.Result) {
	t.Helper()
	if len(res.Chain) == 0 || res.Chain[0] != res.P0 {
		t.Fatalf("chain does not start at P0: %+v", res)
	}
	if res.Chain[len(res.Chain)-1] != res.Final {
		t.Fatalf("chain does not end at Final: %+v", res)
	}
	if res.Hops != len(res.Chain)-1 {
		t.Fatalf("hops %d != len(chain)-1 %d", res.Hops, len(res.Chain)-1)
	}
	if res.Hops > r.K() {
		t.Fatalf("hops %d > K %d", res.Hops, r.K())
	}
	seen := map[string]bool{}
	for _, p := range res.Chain {
		if seen[p] {
			t.Fatalf("chain has duplicate %q: %v", p, res.Chain)
		}
		seen[p] = true
	}
	if v := res.Version; v == 0 || v > r.Version() {
		t.Fatalf("result version %d outside (0, %d]", v, r.Version())
	}
}

func TestConcurrent(t *testing.T) {
	r := mustRouter(t, 4,
		[]route.Entry{
			{Prefix: "/", Backend: "root"},
			{Prefix: "/api", Scope: "api", Backend: "api"},
			{Prefix: "/int", Scope: "admin", Backend: "int"},
		},
		[]rewrite.Rule{
			{From: "/a", To: "/b"},
			{From: "/old", To: "/new", Final: true},
		},
	)
	var wg sync.WaitGroup
	audits := make(chan uint64, 8192)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				switch rng.Intn(10) {
				case 0:
					_ = r.SetRoutes([]route.Entry{
						{Prefix: "/", Backend: "root"},
						{Prefix: "/api", Scope: "api", Backend: "api"},
						{Prefix: "/int", Scope: "admin", Backend: "int"},
					})
				case 1:
					_ = r.SetRules([]rewrite.Rule{
						{From: "/a", To: "/b"},
						{From: "/old", To: "/new", Final: true},
					})
				default:
					res, err := r.Handle(pathPool[rng.Intn(len(pathPool))], genScopes(rng))
					if err == nil {
						checkResultInvariants(t, r, res)
						audits <- res.Audit
					}
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	close(audits)
	seen := map[uint64]bool{}
	count := 0
	for a := range audits {
		if seen[a] {
			t.Fatalf("duplicate audit number %d", a)
		}
		seen[a] = true
		count++
	}
	for a := range seen {
		if a < 1 || a > uint64(count) {
			t.Fatalf("audit %d outside 1..%d (gaps or reuse)", a, count)
		}
	}
}

// ---------- naive simulator ----------
//
// The simulator re-implements the spec step by step with linear scans
// and no shared data structures, as an independent cross-check.

type simRoute struct{ prefix, scope, backend string }

type simRule struct {
	from  string
	to    string
	final bool
}

type simOutcome struct {
	ok      bool
	kind    string // gateway.Kind.String() of the failure
	stage   string // gateway.Stage.String() for denials
	reason  string // decision rationale, printed to the test log
	p0      string
	final   string
	backend string
	hops    int
	chain   []string
	version uint64
	audit   uint64
}

type simulator struct {
	k       int
	routes  []simRoute
	rules   []simRule
	version uint64
	audit   uint64
}

func simNormalize(p string) (string, bool) {
	if len(p) == 0 || p[0] != '/' {
		return "", false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f || c == '%' || c == '?' || c == '#' {
			return "", false
		}
	}
	var st []string
	for i := 0; i <= len(p); {
		j := i
		for j < len(p) && p[j] != '/' {
			j++
		}
		seg := p[i:j]
		switch seg {
		case "", ".":
		case "..":
			if len(st) == 0 {
				return "", false
			}
			st = st[:len(st)-1]
		default:
			st = append(st, seg)
		}
		i = j + 1
	}
	if len(st) == 0 {
		return "/", true
	}
	return "/" + strings.Join(st, "/"), true
}

// simMatch reports whether prefix matches x on a segment boundary and
// returns the rest of x.
func simMatch(prefix, x string) (string, bool) {
	if prefix == "/" {
		if x == "/" {
			return "", true
		}
		return x, true
	}
	if x == prefix {
		return "", true
	}
	if strings.HasPrefix(x, prefix+"/") {
		return x[len(prefix):], true
	}
	return "", false
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

func (s *simulator) setRoutes(entries []simRoute) bool {
	seen := map[string]bool{}
	for _, e := range entries {
		n, ok := simNormalize(e.prefix)
		if !ok || n != e.prefix || e.backend == "" || seen[e.prefix] {
			return false
		}
		seen[e.prefix] = true
	}
	s.routes = append([]simRoute(nil), entries...)
	s.version++
	return true
}

func (s *simulator) setRules(rules []simRule) bool {
	seen := map[string]bool{}
	for _, r := range rules {
		nf, ok1 := simNormalize(r.from)
		nt, ok2 := simNormalize(r.to)
		if !ok1 || nf != r.from || !ok2 || nt != r.to || seen[r.from] {
			return false
		}
		seen[r.from] = true
	}
	s.rules = append([]simRule(nil), rules...)
	s.version++
	return true
}

func (s *simulator) longestRoute(x string) (simRoute, bool) {
	best := -1
	for i, r := range s.routes {
		if _, ok := simMatch(r.prefix, x); ok {
			if best < 0 || len(r.prefix) > len(s.routes[best].prefix) {
				best = i
			}
		}
	}
	if best < 0 {
		return simRoute{}, false
	}
	return s.routes[best], true
}

func (s *simulator) longestRule(x string) (simRule, string, bool) {
	best := -1
	bestRest := ""
	for i, r := range s.rules {
		if rest, ok := simMatch(r.from, x); ok {
			if best < 0 || len(r.from) > len(s.rules[best].from) {
				best, bestRest = i, rest
			}
		}
	}
	if best < 0 {
		return simRule{}, "", false
	}
	return s.rules[best], bestRest, true
}

func (s *simulator) handle(path string, scopes []string) simOutcome {
	for _, sc := range scopes {
		if sc == "" {
			return simOutcome{kind: "bad-argument", reason: "scopes contain empty entry"}
		}
	}
	p0, ok := simNormalize(path)
	if !ok {
		return simOutcome{kind: "bad-path", reason: "path fails normalization"}
	}
	if e, ok := s.longestRoute(p0); ok && e.scope != "" && !hasScope(scopes, e.scope) {
		return simOutcome{kind: "denied", stage: "origin",
			reason: fmt.Sprintf("origin route %s requires scope %q", e.prefix, e.scope)}
	}
	chain := []string{p0}
	seen := map[string]bool{p0: true}
	cur := p0
	for {
		rule, rest, ok := s.longestRule(cur)
		if !ok {
			break
		}
		if len(chain)-1 == s.k {
			return simOutcome{kind: "hop-limit",
				reason: fmt.Sprintf("%s still matches rule %s after %d hops (K=%d)", cur, rule.from, s.k, s.k)}
		}
		next, ok := simNormalize(rule.to + rest)
		if !ok {
			panic("simulator: unnormalizable rewrite target")
		}
		if seen[next] {
			return simOutcome{kind: "loop",
				reason: fmt.Sprintf("step %d reproduces %s", len(chain), next)}
		}
		seen[next] = true
		chain = append(chain, next)
		cur = next
		if rule.final {
			break
		}
	}
	e, ok := s.longestRoute(cur)
	if !ok {
		return simOutcome{kind: "no-route", reason: "no route for terminal path " + cur}
	}
	if e.scope != "" && !hasScope(scopes, e.scope) {
		return simOutcome{kind: "denied", stage: "final",
			reason: fmt.Sprintf("final route %s requires scope %q", e.prefix, e.scope)}
	}
	s.audit++
	return simOutcome{
		ok: true, reason: "ok",
		p0: p0, final: cur, backend: e.backend,
		hops: len(chain) - 1, chain: chain,
		version: s.version, audit: s.audit,
	}
}

// ---------- random replay against the simulator ----------

var routePool = []route.Entry{
	{Prefix: "/pub", Backend: "pub"},
	{Prefix: "/api", Scope: "api", Backend: "api"},
	{Prefix: "/int", Scope: "admin", Backend: "int"},
	{Prefix: "/new", Backend: "new"},
	{Prefix: "/x", Backend: "x"},
	{Prefix: "/", Backend: "root"},
	{Prefix: "/pub/old", Scope: "old", Backend: "oldbe"},
	{Prefix: "/a", Backend: "abe"},
	{Prefix: "/b", Backend: "bbe"},
	{Prefix: "/int/x", Backend: "intx"},
}

var rulePool = []rewrite.Rule{
	{From: "/pub/old", To: "/int/x"},
	{From: "/api/legacy", To: "/pub/n"},
	{From: "/a", To: "/b"},
	{From: "/b", To: "/a"},
	{From: "/old", To: "/new", Final: true},
	{From: "/new", To: "/x"},
	{From: "/c", To: "/"},
	{From: "/d", To: "/d"},
	{From: "/x", To: "/new"},
}

var pathPool = []string{
	"/pub/old/y", "/api/legacy/z", "/pub/../api//legacy/./z/", "/pubx",
	"/a", "/b", "/old/k", "/new/k", "/x", "/c/deep", "/d", "/int/x",
	"/", "/pub", "/api", "/./a//b/", "/a/b/../../c", "/new",
	"relative", "/%", "/a?b", "/a#b", "/a\x01b", "/..", "/a/../../b",
}

func genRoutes(rng *rand.Rand) []route.Entry {
	n := rng.Intn(5)
	out := make([]route.Entry, 0, n+1)
	for i := 0; i < n; i++ {
		out = append(out, routePool[rng.Intn(len(routePool))])
	}
	switch rng.Intn(8) {
	case 0:
		out = append(out, route.Entry{Prefix: "/zzz/", Backend: "z"}) // not normalized
	case 1:
		out = append(out, route.Entry{Prefix: "/zzz"}) // empty backend
	case 2:
		if len(out) > 0 {
			out = append(out, out[0]) // duplicate
		}
	}
	return out
}

func genRules(rng *rand.Rand) []rewrite.Rule {
	n := rng.Intn(5)
	out := make([]rewrite.Rule, 0, n+1)
	for i := 0; i < n; i++ {
		out = append(out, rulePool[rng.Intn(len(rulePool))])
	}
	switch rng.Intn(8) {
	case 0:
		out = append(out, rewrite.Rule{From: "/q/", To: "/z"}) // from not normalized
	case 1:
		out = append(out, rewrite.Rule{From: "/q", To: "//z"}) // to not normalized
	case 2:
		if len(out) > 0 {
			out = append(out, out[0]) // duplicate from
		}
	}
	return out
}

func genScopes(rng *rand.Rand) []string {
	pool := []string{"api", "admin", "old", "user"}
	var out []string
	for _, s := range pool {
		if rng.Intn(3) == 0 {
			out = append(out, s)
		}
	}
	if rng.Intn(20) == 0 {
		out = append(out, "") // invalid: empty scope
	}
	return out
}

func toSimRoutes(entries []route.Entry) []simRoute {
	out := make([]simRoute, len(entries))
	for i, e := range entries {
		out[i] = simRoute{prefix: e.Prefix, scope: e.Scope, backend: e.Backend}
	}
	return out
}

func toSimRules(rules []rewrite.Rule) []simRule {
	out := make([]simRule, len(rules))
	for i, r := range rules {
		out[i] = simRule{from: r.From, to: r.To, final: r.Final}
	}
	return out
}

func compareHandle(t *testing.T, seq, op int, path string, scopes []string,
	got gateway.Result, gerr error, want simOutcome) {
	t.Helper()
	log := fmt.Sprintf("seq=%d op=%d Handle(%q, %v)", seq, op, path, scopes)
	if want.ok {
		if gerr != nil {
			t.Fatalf("%s\n  gateway error: %v\n  simulator: ok (%s)", log, gerr, want.reason)
		}
		problems := []string{}
		if got.P0 != want.p0 {
			problems = append(problems, "P0")
		}
		if got.Final != want.final {
			problems = append(problems, "Final")
		}
		if got.Backend != want.backend {
			problems = append(problems, "Backend")
		}
		if got.Hops != want.hops {
			problems = append(problems, "Hops")
		}
		if !reflect.DeepEqual(got.Chain, want.chain) {
			problems = append(problems, "Chain")
		}
		if got.Version != want.version {
			problems = append(problems, "Version")
		}
		if got.Audit != want.audit {
			problems = append(problems, "Audit")
		}
		if len(problems) > 0 {
			t.Fatalf("%s\n  mismatch %v\n  gateway:   %+v\n  simulator: %+v",
				log, problems, got, want)
		}
		t.Logf("%s -> ok backend=%s hops=%d chain=%v version=%d audit=%d (%s)",
			log, got.Backend, got.Hops, got.Chain, got.Version, got.Audit, want.reason)
		return
	}
	ge, ok := gerr.(*gateway.Error)
	if !ok {
		t.Fatalf("%s\n  gateway: %+v\n  simulator: %s/%s (%s)", log, got, want.kind, want.stage, want.reason)
	}
	if ge.Kind.String() != want.kind {
		t.Fatalf("%s\n  gateway kind: %s\n  simulator: %s (%s)", log, ge.Kind, want.kind, want.reason)
	}
	if want.kind == "denied" && ge.Stage.String() != want.stage {
		t.Fatalf("%s\n  gateway stage: %s\n  simulator: %s (%s)", log, ge.Stage, want.stage, want.reason)
	}
	t.Logf("%s -> %s/%s (%s)", log, want.kind, want.stage, want.reason)
}

func TestRandomReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	for seq := 0; seq < 2000; seq++ {
		k := rng.Intn(6) // 0..5, exercises K=0 and small limits
		r, err := gateway.NewRouter(k)
		if err != nil {
			t.Fatal(err)
		}
		sim := &simulator{k: k}
		ops := 1 + rng.Intn(12)
		for op := 0; op < ops; op++ {
			switch rng.Intn(10) {
			case 0, 1:
				entries := genRoutes(rng)
				gerr := r.SetRoutes(entries)
				sok := sim.setRoutes(toSimRoutes(entries))
				if (gerr == nil) != sok {
					t.Fatalf("seq=%d op=%d SetRoutes(%v): gateway err=%v, simulator ok=%v",
						seq, op, entries, gerr, sok)
				}
				if r.Version() != sim.version {
					t.Fatalf("seq=%d op=%d SetRoutes: version %d != sim %d",
						seq, op, r.Version(), sim.version)
				}
				t.Logf("seq=%d op=%d SetRoutes(%d entries) -> ok=%v version=%d",
					seq, op, len(entries), sok, sim.version)
			case 2, 3:
				rules := genRules(rng)
				gerr := r.SetRules(rules)
				sok := sim.setRules(toSimRules(rules))
				if (gerr == nil) != sok {
					t.Fatalf("seq=%d op=%d SetRules(%v): gateway err=%v, simulator ok=%v",
						seq, op, rules, gerr, sok)
				}
				if r.Version() != sim.version {
					t.Fatalf("seq=%d op=%d SetRules: version %d != sim %d",
						seq, op, r.Version(), sim.version)
				}
				t.Logf("seq=%d op=%d SetRules(%d rules) -> ok=%v version=%d",
					seq, op, len(rules), sok, sim.version)
			default:
				path := pathPool[rng.Intn(len(pathPool))]
				scopes := genScopes(rng)
				got, gerr := r.Handle(path, scopes)
				want := sim.handle(path, scopes)
				compareHandle(t, seq, op, path, scopes, got, gerr, want)
				if gerr == nil {
					checkResultInvariants(t, r, got)
				}
			}
		}
	}
}
