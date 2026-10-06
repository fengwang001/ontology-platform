package cors

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func baseConfig() Config {
	return Config{
		SafeMethods: []string{"GET", "HEAD", "POST"},
		SafeHeaders: map[string]HeaderRule{
			"accept": {MaxLength: 8, AllowedChars: "abc"},
			"x-safe": {MaxLength: 4, AllowedChars: "0123456789"},
			"X-Mix":  {MaxLength: 4, AllowedChars: "ab"},
		},
		SafeResponseHeaders: []string{"content-type"},
		DefaultMaxAge:       5,
		MaxMaxAge:           100,
		Capacity:            8,
	}
}

func newTestEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	eng.SetLogger(func(s string) { t.Log(s) })
	return eng
}

func req(origin, target, method string, headers []Header, creds bool) Request {
	return Request{Origin: NewOrigin(origin), Target: target, Method: method, Headers: headers, IncludeCredentials: creds}
}

func i64(v int64) *int64 { return &v }

func mustDecide(t *testing.T, eng *Engine, r Request) Decision {
	t.Helper()
	d, err := eng.Decide(r)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d
}

func TestSimpleConditions(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	safe := []Header{{Name: "accept", Value: "abc"}}

	if d := mustDecide(t, eng, req("https://a", "https://b", "GET", safe, false)); d.Kind != DecisionSimple {
		t.Fatalf("all-safe => want simple, got %v", d.Kind)
	}
	if d := mustDecide(t, eng, req("https://a", "https://b", "DELETE", safe, false)); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("unsafe method => want preflight, got %v", d.Kind)
	}
	badName := []Header{{Name: "x-other", Value: "abc"}}
	if d := mustDecide(t, eng, req("https://a", "https://b", "GET", badName, false)); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("unsafe header name => want preflight, got %v", d.Kind)
	}
	tooLong := []Header{{Name: "accept", Value: "abcabcabc"}}
	if d := mustDecide(t, eng, req("https://a", "https://b", "GET", tooLong, false)); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("value too long => want preflight, got %v", d.Kind)
	}
	badChar := []Header{{Name: "accept", Value: "abz"}}
	if d := mustDecide(t, eng, req("https://a", "https://b", "GET", badChar, false)); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("bad char => want preflight, got %v", d.Kind)
	}
}

func TestHeaderNameCaseInsensitivity(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	mixed := []Header{{Name: "x-mix", Value: "ab"}}
	if d := mustDecide(t, eng, req("https://a", "https://b", "GET", mixed, false)); d.Kind != DecisionSimple {
		t.Fatalf("config X-Mix vs request x-mix => want simple, got %v", d.Kind)
	}
	r := req("https://a", "https://b", "PUT", []Header{{Name: "X-Custom", Value: "1"}}, false)
	err := eng.SubmitPreflight(r, PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"PUT"},
		AllowHeaders: []string{"X-CUSTOM"},
		MaxAge:       i64(10),
	})
	if err != nil {
		t.Fatalf("SubmitPreflight: %v", err)
	}
	if d := mustDecide(t, eng, r); d.Kind != DecisionCacheHit {
		t.Fatalf("case-insensitive allow-headers => want cache hit, got %v", d.Kind)
	}
	snap := eng.Snapshot()
	if len(snap) != 1 || !reflect.DeepEqual(snap[0].Headers, []string{"x-custom"}) {
		t.Fatalf("cache must store normalized names, got %+v", snap)
	}
}

func TestMaxAgeZeroAndNegative(t *testing.T) {
	for _, age := range []int64{0, -3} {
		eng := newTestEngine(t, baseConfig())
		r := req("https://a", "https://b", "PUT", []Header{{Name: "x-c", Value: "1"}}, false)
		err := eng.SubmitPreflight(r, PreflightResponse{
			AllowOrigin:  "https://a",
			AllowMethods: []string{"PUT"},
			AllowHeaders: []string{"x-c"},
			MaxAge:       i64(age),
		})
		if err != nil {
			t.Fatalf("age=%d: preflight itself must succeed, got %v", age, err)
		}
		if n := eng.EntryCount(); n != 0 {
			t.Fatalf("age=%d: must not cache, got %d entries", age, n)
		}
		if d := mustDecide(t, eng, r); d.Kind != DecisionPreflightNeeded {
			t.Fatalf("age=%d: result only for current request, got %v", age, d.Kind)
		}
	}
}

func TestMaxAgeClamped(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-c", Value: "1"}}, false)
	err := eng.SubmitPreflight(r, PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"PUT"},
		AllowHeaders: []string{"x-c"},
		MaxAge:       i64(1000),
	})
	if err != nil {
		t.Fatalf("SubmitPreflight: %v", err)
	}
	snap := eng.Snapshot()
	if len(snap) != 1 || snap[0].Expiry != 100 {
		t.Fatalf("expiry must be clamped to MaxMaxAge=100, got %+v", snap)
	}
}

func TestExpiryEqualsNow(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-c", Value: "1"}}, false)
	err := eng.SubmitPreflight(r, PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"PUT"},
		AllowHeaders: []string{"x-c"},
		MaxAge:       i64(5),
	})
	if err != nil {
		t.Fatalf("SubmitPreflight: %v", err)
	}
	if err := eng.AdvanceClock(4); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	if d := mustDecide(t, eng, r); d.Kind != DecisionCacheHit {
		t.Fatalf("now=4 < expiry=5 => want cache hit, got %v", d.Kind)
	}
	if err := eng.AdvanceClock(1); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	if d := mustDecide(t, eng, r); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("now=5 == expiry=5 => expired, want preflight, got %v", d.Kind)
	}
}

func TestCredentialsWildcardOriginInvalid(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-c", Value: "1"}}, true)
	err := eng.SubmitPreflight(r, PreflightResponse{
		AllowOrigin:      "*",
		AllowCredentials: true,
		AllowMethods:     []string{"PUT"},
		AllowHeaders:     []string{"x-c"},
	})
	if err == nil || err.Kind != ErrKindPreflightFailed || err.Reason != ReasonOriginMismatch {
		t.Fatalf("wildcard origin with credentials => want origin-mismatch, got %v", err)
	}
}

func TestCredentialsWildcardMethodHeaderInvalid(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-c", Value: "1"}}, true)
	resp := PreflightResponse{
		AllowOrigin:      "https://a",
		AllowCredentials: true,
		AllowAnyMethod:   true,
		AllowAnyHeader:   true,
	}
	err := eng.SubmitPreflight(r, resp)
	if err == nil || err.Reason != ReasonMethodMismatch {
		t.Fatalf("wildcard method must not take effect with credentials, got %v", err)
	}
	resp.AllowMethods = []string{"PUT"}
	err = eng.SubmitPreflight(r, resp)
	if err == nil || err.Reason != ReasonHeaderMismatch {
		t.Fatalf("wildcard header must not take effect with credentials, got %v", err)
	}
	resp.AllowHeaders = []string{"X-C"}
	if err := eng.SubmitPreflight(r, resp); err != nil {
		t.Fatalf("enumerated lists must work with credentials, got %v", err)
	}
	snap := eng.Snapshot()
	if len(snap) != 1 || snap[0].AnyMethod || snap[0].AnyHeader {
		t.Fatalf("wildcards must be stored as ineffective, got %+v", snap)
	}
}

func TestCredentialsWildcardExposeInvalid(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	headers := []Header{{Name: "X-Secret", Value: "v"}, {Name: "Content-Type", Value: "text/plain"}}
	r := req("https://a", "https://b", "GET", nil, true)
	res, err := eng.SubmitActual(r, ActualResponse{
		AllowOrigin:      "https://a",
		AllowCredentials: true,
		ExposeAnyHeader:  true,
		Headers:          headers,
	})
	if err != nil {
		t.Fatalf("SubmitActual: %v", err)
	}
	if len(res.Exposed) != 1 || res.Exposed[0].Name != "Content-Type" {
		t.Fatalf("expose wildcard invalid with credentials, got %+v", res.Exposed)
	}
	rNoCreds := req("https://a", "https://b", "GET", nil, false)
	res, err = eng.SubmitActual(rNoCreds, ActualResponse{
		AllowOrigin:     "https://a",
		ExposeAnyHeader: true,
		Headers:         headers,
	})
	if err != nil {
		t.Fatalf("SubmitActual: %v", err)
	}
	if len(res.Exposed) != 2 {
		t.Fatalf("expose wildcard valid without credentials, got %+v", res.Exposed)
	}
}

func TestPartialCoverageMerge(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r1 := req("https://a", "https://b", "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
	err := eng.SubmitPreflight(r1, PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"PUT"},
		AllowHeaders: []string{"x-a"},
		MaxAge:       i64(50),
	})
	if err != nil {
		t.Fatalf("SubmitPreflight r1: %v", err)
	}
	r2 := req("https://a", "https://b", "DELETE", []Header{{Name: "x-b", Value: "1"}}, false)
	if d := mustDecide(t, eng, r2); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("partial coverage => want preflight, got %v", d.Kind)
	}
	err = eng.SubmitPreflight(r2, PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"DELETE"},
		AllowHeaders: []string{"x-b"},
		MaxAge:       i64(30),
	})
	if err != nil {
		t.Fatalf("SubmitPreflight r2: %v", err)
	}
	snap := eng.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("merge must keep single entry, got %+v", snap)
	}
	got := snap[0]
	if !reflect.DeepEqual(got.Methods, []string{"DELETE", "PUT"}) ||
		!reflect.DeepEqual(got.Headers, []string{"x-a", "x-b"}) ||
		got.Expiry != 30 {
		t.Fatalf("merged entry wrong: %+v", got)
	}
	r3 := req("https://a", "https://b", "DELETE", []Header{{Name: "x-a", Value: "1"}}, false)
	if d := mustDecide(t, eng, r3); d.Kind != DecisionCacheHit {
		t.Fatalf("merged entry must cover DELETE+x-a, got %v", d.Kind)
	}
	r4 := req("https://a", "https://b", "PUT", []Header{{Name: "x-b", Value: "1"}}, false)
	if d := mustDecide(t, eng, r4); d.Kind != DecisionCacheHit {
		t.Fatalf("merged entry must cover PUT+x-b, got %v", d.Kind)
	}
}

func TestPreflightFailureKeepsEntry(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
	good := PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"PUT"},
		AllowHeaders: []string{"x-a"},
		MaxAge:       i64(50),
	}
	if err := eng.SubmitPreflight(r, good); err != nil {
		t.Fatalf("SubmitPreflight: %v", err)
	}
	before := eng.Snapshot()
	bad := good
	bad.AllowMethods = []string{"GET"}
	err := eng.SubmitPreflight(r, bad)
	if err == nil || err.Kind != ErrKindPreflightFailed || err.Reason != ReasonMethodMismatch {
		t.Fatalf("want method-mismatch, got %v", err)
	}
	if after := eng.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed preflight must not change cache:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestPreflightFailureOrder(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-c", Value: "1"}}, true)
	resp := PreflightResponse{
		AllowOrigin:      "https://evil",
		AllowCredentials: false,
		AllowMethods:     []string{"GET"},
		AllowHeaders:     nil,
	}
	wantReasons := []PreflightFailReason{
		ReasonOriginMismatch,
		ReasonCredentialsMismatch,
		ReasonMethodMismatch,
		ReasonHeaderMismatch,
	}
	fixes := []func(){
		func() { resp.AllowOrigin = "https://a" },
		func() { resp.AllowCredentials = true },
		func() { resp.AllowMethods = []string{"PUT"} },
		func() { resp.AllowHeaders = []string{"x-c"} },
	}
	for i, want := range wantReasons {
		err := eng.SubmitPreflight(r, resp)
		if err == nil || err.Kind != ErrKindPreflightFailed || err.Reason != want {
			t.Fatalf("step %d: want %v, got %v", i, want, err)
		}
		fixes[i]()
	}
	if err := eng.SubmitPreflight(r, resp); err != nil {
		t.Fatalf("all fixed => want success, got %v", err)
	}
}

func TestActualFailureKeepsCache(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
	if err := eng.SubmitPreflight(r, PreflightResponse{
		AllowOrigin:  "https://a",
		AllowMethods: []string{"PUT"},
		AllowHeaders: []string{"x-a"},
		MaxAge:       i64(50),
	}); err != nil {
		t.Fatalf("SubmitPreflight: %v", err)
	}
	before := eng.Snapshot()
	_, err := eng.SubmitActual(r, ActualResponse{AllowOrigin: "https://evil"})
	if err == nil || err.Kind != ErrKindResponseValidationFailed || err.Reason != ReasonOriginMismatch {
		t.Fatalf("want response-validation/origin-mismatch, got %v", err)
	}
	_, err = eng.SubmitActual(req("https://a", "https://b", "PUT", nil, true), ActualResponse{AllowOrigin: "https://a"})
	if err == nil || err.Kind != ErrKindResponseValidationFailed || err.Reason != ReasonCredentialsMismatch {
		t.Fatalf("want response-validation/credentials-mismatch, got %v", err)
	}
	if after := eng.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed actual response must not change cache:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestRedirectOpaqueOrigin(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "GET", nil, false)
	res, err := eng.SubmitActual(r, ActualResponse{RedirectTo: "https://c"})
	if err != nil || !res.Redirect {
		t.Fatalf("want redirect, got %+v %v", res, err)
	}
	if !res.FollowUp.Origin.IsOpaque() || res.FollowUp.Target != "https://c" {
		t.Fatalf("cross-origin redirect must yield opaque origin, got %+v", res.FollowUp)
	}
	back, err := eng.SubmitActual(res.FollowUp, ActualResponse{RedirectTo: "https://a"})
	if err != nil {
		t.Fatalf("SubmitActual: %v", err)
	}
	if !back.FollowUp.Origin.IsOpaque() {
		t.Fatalf("redirect chain must stay opaque, got %+v", back.FollowUp)
	}
	if back.FollowUp.Origin.Equal(NewOrigin("https://a")) {
		t.Fatal("opaque origin must not equal its literal target")
	}
	if res.FollowUp.Origin.Equal(back.FollowUp.Origin) {
		t.Fatal("two opaque origins must not be equal")
	}
	nonSimple := back.FollowUp
	nonSimple.Method = "PUT"
	if d := mustDecide(t, eng, nonSimple); d.Kind != DecisionPreflightNeeded {
		t.Fatalf("opaque origin to own literal origin => still cross-origin, got %v", d.Kind)
	}
	if err := eng.SubmitPreflight(nonSimple, PreflightResponse{
		AllowOrigin:  "*",
		AllowMethods: []string{"PUT"},
		MaxAge:       i64(10),
	}); err != nil {
		t.Fatalf("preflight for opaque origin: %v", err)
	}
	if d := mustDecide(t, eng, nonSimple); d.Kind != DecisionCacheHit {
		t.Fatalf("opaque origin cache key must hit, got %v", d.Kind)
	}
	same, err := eng.SubmitActual(r, ActualResponse{RedirectTo: "https://b"})
	if err != nil || !same.Redirect {
		t.Fatalf("want redirect, got %+v %v", same, err)
	}
	if same.FollowUp.Origin.IsOpaque() {
		t.Fatal("same-origin redirect must keep origin")
	}
}

func TestPreflightRedirectFails(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	r := req("https://a", "https://b", "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
	err := eng.SubmitPreflight(r, PreflightResponse{Redirected: true, AllowOrigin: "https://a"})
	if err == nil || err.Kind != ErrKindRedirectNotAllowed {
		t.Fatalf("preflight redirect => want redirect-not-allowed, got %v", err)
	}
	if n := eng.EntryCount(); n != 0 {
		t.Fatalf("rejected preflight must not cache, got %d entries", n)
	}
}

func TestEvictionAndTieBreak(t *testing.T) {
	cfg := baseConfig()
	cfg.Capacity = 2
	eng := newTestEngine(t, cfg)
	preflight := func(target string) {
		t.Helper()
		r := req("https://a", target, "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
		if err := eng.SubmitPreflight(r, PreflightResponse{
			AllowOrigin:  "https://a",
			AllowMethods: []string{"PUT"},
			AllowHeaders: []string{"x-a"},
			MaxAge:       i64(100),
		}); err != nil {
			t.Fatalf("preflight %s: %v", target, err)
		}
	}
	targets := func() []string {
		var out []string
		for _, s := range eng.Snapshot() {
			out = append(out, s.Target)
		}
		return out
	}
	preflight("https://t1")
	preflight("https://t2")
	preflight("https://t3")
	if got := targets(); !reflect.DeepEqual(got, []string{"https://t2", "https://t3"}) {
		t.Fatalf("tie on lastHit => evict oldest created (t1), got %v", got)
	}
	if err := eng.AdvanceClock(1); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	r2 := req("https://a", "https://t2", "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
	if d := mustDecide(t, eng, r2); d.Kind != DecisionCacheHit {
		t.Fatalf("want hit on t2, got %v", d.Kind)
	}
	preflight("https://t4")
	if got := targets(); !reflect.DeepEqual(got, []string{"https://t2", "https://t4"}) {
		t.Fatalf("least recently hit (t3) must be evicted, got %v", got)
	}
	if err := eng.AdvanceClock(1000); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	preflight("https://t5")
	if got := targets(); !reflect.DeepEqual(got, []string{"https://t5"}) {
		t.Fatalf("expired entries must be cleared first, got %v", got)
	}
}

func TestPurgeByOriginAndTarget(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	add := func(origin, target string) {
		t.Helper()
		r := req(origin, target, "PUT", []Header{{Name: "x-a", Value: "1"}}, false)
		if err := eng.SubmitPreflight(r, PreflightResponse{
			AllowOrigin:  origin,
			AllowMethods: []string{"PUT"},
			AllowHeaders: []string{"x-a"},
			MaxAge:       i64(100),
		}); err != nil {
			t.Fatalf("preflight: %v", err)
		}
	}
	add("https://o1", "https://t1")
	add("https://o1", "https://t2")
	add("https://o2", "https://t1")
	if n := eng.PurgeByOrigin(NewOrigin("https://o1")); n != 2 {
		t.Fatalf("purge by origin => want 2, got %d", n)
	}
	if n := eng.EntryCount(); n != 1 {
		t.Fatalf("want 1 entry left, got %d", n)
	}
	if n := eng.PurgeByTarget("https://t1"); n != 1 {
		t.Fatalf("purge by target => want 1, got %d", n)
	}
	if n := eng.EntryCount(); n != 0 {
		t.Fatalf("want 0 entries left, got %d", n)
	}
}

func TestInvalidArguments(t *testing.T) {
	badConfigs := []Config{
		func() Config { c := baseConfig(); c.Capacity = 0; return c }(),
		func() Config { c := baseConfig(); c.MaxMaxAge = 0; return c }(),
		func() Config { c := baseConfig(); c.DefaultMaxAge = -1; return c }(),
		func() Config {
			c := baseConfig()
			c.SafeHeaders = map[string]HeaderRule{"x-a": {MaxLength: 0, AllowedChars: "a"}}
			return c
		}(),
		func() Config {
			c := baseConfig()
			c.SafeHeaders = map[string]HeaderRule{"bad name": {MaxLength: 1, AllowedChars: "a"}}
			return c
		}(),
	}
	for i, cfg := range badConfigs {
		if _, err := NewEngine(cfg); err == nil || err.Kind != ErrKindInvalidArgument {
			t.Fatalf("config %d: want invalid-argument, got %v", i, err)
		}
	}
	eng := newTestEngine(t, baseConfig())
	badReqs := []Request{
		req("", "https://b", "GET", nil, false),
		req("https://a", "", "GET", nil, false),
		req("https://a", "https://b", "", nil, false),
		req("https://a", "https://b", "GET", []Header{{Name: "bad name", Value: "1"}}, false),
	}
	for i, r := range badReqs {
		if _, err := eng.Decide(r); err == nil || err.Kind != ErrKindInvalidArgument {
			t.Fatalf("request %d: want invalid-argument, got %v", i, err)
		}
	}
	r := req("https://a", "https://b", "", nil, false)
	err := eng.SubmitPreflight(r, PreflightResponse{Redirected: true})
	if err == nil || err.Kind != ErrKindInvalidArgument {
		t.Fatalf("invalid argument must precede redirect-not-allowed, got %v", err)
	}
}

func TestClockRollback(t *testing.T) {
	eng := newTestEngine(t, baseConfig())
	if err := eng.AdvanceClock(5); err != nil {
		t.Fatalf("AdvanceClock: %v", err)
	}
	err := eng.AdvanceClock(-1)
	if err == nil || err.Kind != ErrKindClockRollback {
		t.Fatalf("want clock-rollback, got %v", err)
	}
	if now := eng.Now(); now != 5 {
		t.Fatalf("rejected rollback must not move clock, got %d", now)
	}
}

func TestConcurrent(t *testing.T) {
	cfg := baseConfig()
	eng := newTestEngine(t, cfg)
	origins := []string{"https://o1", "https://o2", "https://o3"}
	targets := []string{"https://t1", "https://t2", "https://t3", "https://t4"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				origin := origins[(g+i)%len(origins)]
				target := targets[(g+i)%len(targets)]
				r := req(origin, target, "PUT", []Header{{Name: "x-a", Value: "1"}}, i%2 == 0)
				switch i % 5 {
				case 0:
					eng.Decide(r)
				case 1:
					eng.SubmitPreflight(r, PreflightResponse{
						AllowOrigin:      origin,
						AllowCredentials: true,
						AllowMethods:     []string{"PUT"},
						AllowHeaders:     []string{"x-a"},
						MaxAge:           i64(3),
					})
				case 2:
					eng.SubmitActual(r, ActualResponse{AllowOrigin: origin, AllowCredentials: true})
				case 3:
					eng.AdvanceClock(1)
				case 4:
					eng.PurgeByTarget(target)
				}
			}
		}(g)
	}
	wg.Wait()
	if n := eng.EntryCount(); n > cfg.Capacity {
		t.Fatalf("entry count %d exceeds capacity %d", n, cfg.Capacity)
	}
}

func TestLogging(t *testing.T) {
	eng, err := NewEngine(baseConfig())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	var lines []string
	eng.SetLogger(func(s string) { lines = append(lines, s) })
	if _, err := eng.Decide(req("https://a", "https://b", "GET", nil, false)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("logger must receive decision lines")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"DECIDE", "https://a", "https://b", "GET", "simple"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("log must contain %q, got:\n%s", want, joined)
		}
	}
}
