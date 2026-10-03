package ontology

import (
	"strings"
	"sync"
	"testing"

	"ontology/rewrite"
	"ontology/route"
)

func baseRoutes() []route.Route {
	return []route.Route{
		{Prefix: "/pub", Scope: "", Backend: "pub"},
		{Prefix: "/api", Scope: "api", Backend: "api"},
		{Prefix: "/int", Scope: "admin", Backend: "int"},
		{Prefix: "/a", Scope: "", Backend: "a"},
		{Prefix: "/b", Scope: "", Backend: "b"},
		{Prefix: "/old", Scope: "", Backend: "old"},
		{Prefix: "/new", Scope: "", Backend: "new"},
		{Prefix: "/x", Scope: "", Backend: "x"},
	}
}

func TestConcurrentCallsAndReplacements(t *testing.T) {
	router := configuredRouter(t, 3)
	var wait sync.WaitGroup
	results := make(chan uint64, 200)
	for worker := 0; worker < 20; worker++ {
		wait.Add(2)
		go func(id int) {
			defer wait.Done()
			for i := 0; i < 5; i++ {
				result, err := router.Handle("/new/k", nil)
				if err != nil {
					t.Errorf("handle: %v", err)
					return
				}
				results <- result.Audit
			}
		}(worker)
		go func(id int) {
			defer wait.Done()
			for i := 0; i < 5; i++ {
				if err := router.SetRoutes(baseRoutes()); err != nil {
					t.Errorf("routes: %v", err)
					return
				}
				if err := router.SetRules(baseRules()); err != nil {
					t.Errorf("rules: %v", err)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
	close(results)
	seen := map[uint64]bool{}
	for audit := range results {
		if audit == 0 || seen[audit] {
			t.Fatalf("bad audit %d seen=%v", audit, seen)
		}
		seen[audit] = true
	}
	if len(seen) != 100 {
		t.Fatalf("unique successes=%d", len(seen))
	}
}

func baseRules() []rewrite.Rule {
	return []rewrite.Rule{
		{From: "/pub/old", To: "/int/x"},
		{From: "/api/legacy", To: "/pub/n"},
		{From: "/a", To: "/b"},
		{From: "/b", To: "/a"},
		{From: "/old", To: "/new", Final: true},
		{From: "/new", To: "/x"},
	}
}

func configuredRouter(t *testing.T, k int) *Router {
	t.Helper()
	router, err := NewRouter(k)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.SetRoutes(baseRoutes()); err != nil {
		t.Fatal(err)
	}
	if err := router.SetRules(baseRules()); err != nil {
		t.Fatal(err)
	}
	return router
}

func failure(t *testing.T, err error, kind, stage string) {
	t.Helper()
	var gatewayErr *Error
	if err == nil {
		t.Fatalf("expected %s/%s, got success", kind, stage)
	}
	if !AsError(err, &gatewayErr) || gatewayErr.Kind != kind || gatewayErr.Stage != stage {
		t.Fatalf("err=%v want %s/%s", err, kind, stage)
	}
}

func AsError(err error, target **Error) bool {
	for err != nil {
		if gatewayErr, ok := err.(*Error); ok {
			*target = gatewayErr
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

func TestHandleSpecCases(t *testing.T) {
	cases := []struct {
		name    string
		k       int
		path    string
		scopes  []string
		kind    string
		stage   string
		backend string
		final   string
		hops    int
	}{
		{"public original protected final", 3, "/pub/old/y", nil, "unauthorized", "final", "", "", 1},
		{"double auth success", 3, "/pub/old/y", []string{"admin"}, "", "", "int", "/int/x/y", 1},
		{"original auth first", 3, "/api/legacy/z", nil, "unauthorized", "original", "", "", 0},
		{"protected original public final", 3, "/api/legacy/z", []string{"api"}, "", "", "pub", "/pub/n/z", 1},
		{"normalize before handle", 3, "/pub/../api//legacy/./z/", []string{"api"}, "", "", "pub", "/pub/n/z", 1},
		{"k one hop limit before loop", 1, "/a", nil, "hop_limit", "rewrite", "", "", 1},
		{"k five loop", 5, "/a", nil, "loop", "rewrite", "", "", 1},
		{"no route", 3, "/pubx", nil, "no_route", "final", "", "", 0},
		{"final stops", 3, "/old/k", nil, "", "", "new", "/new/k", 1},
		{"without final continues", 3, "/new/k", nil, "", "", "x", "/x/k", 1},
		{"zero hops with matching rule", 0, "/old/k", nil, "hop_limit", "rewrite", "", "", 0},
		{"hops equal k success", 1, "/new/k", nil, "", "", "x", "/x/k", 1},
		{"invalid path", 3, "/a%2f", nil, "invalid_path", "path", "", "", 0},
		{"invalid scope first", 3, "/a%2f", []string{""}, "invalid_argument", "argument", "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := configuredRouter(t, tc.k)
			result, err := router.Handle(tc.path, tc.scopes)
			if tc.kind != "" {
				failure(t, err, tc.kind, tc.stage)
				if router.nextAudit != 0 {
					t.Fatalf("failure consumed audit %d", router.nextAudit)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Backend != tc.backend || result.FinalPath != tc.final || result.Hops != tc.hops {
				t.Fatalf("result=%+v want backend=%s final=%s hops=%d", result, tc.backend, tc.final, tc.hops)
			}
			if result.Audit != 1 || result.Version != 2 {
				t.Fatalf("audit/version = %d/%d", result.Audit, result.Version)
			}
		})
	}
}

func TestAtomicReplacementFailure(t *testing.T) {
	router := configuredRouter(t, 3)
	badRoutes := []route.Route{{Prefix: "/not-normalized/", Backend: "x"}}
	if err := router.SetRoutes(badRoutes); err == nil {
		t.Fatal("expected SetRoutes failure")
	}
	badRules := []rewrite.Rule{{From: "/a", To: "/b"}, {From: "/a", To: "/c"}}
	if err := router.SetRules(badRules); err == nil {
		t.Fatal("expected SetRules failure")
	}
	result, err := router.Handle("/old/k", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != 2 || result.Audit != 1 || result.Backend != "new" {
		t.Fatalf("rejected replacement changed state: %+v", result)
	}
}

func TestAuditSequenceOnlySuccess(t *testing.T) {
	router := configuredRouter(t, 3)
	for i := 1; i <= 3; i++ {
		_, _ = router.Handle("/api/legacy/z", nil)
		result, err := router.Handle("/new/k", nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Audit != uint64(i) {
			t.Fatalf("audit=%d want %d", result.Audit, i)
		}
	}
}

func TestNewRouterValidation(t *testing.T) {
	for _, k := range []int{-1, 33} {
		if _, err := NewRouter(k); err != ErrInvalidArgument {
			t.Fatalf("k=%d err=%v", k, err)
		}
	}
}

func TestErrorChainsAreUniqueAndBounded(t *testing.T) {
	router := configuredRouter(t, 2)
	_, err := router.Handle("/a", nil)
	var gatewayErr *Error
	if !AsError(err, &gatewayErr) || gatewayErr.Kind != "loop" {
		t.Fatalf("err=%v", err)
	}
	for _, item := range gatewayErr.Chain {
		if strings.HasSuffix(item, "/") && item != "/" {
			t.Fatalf("bad chain %q", item)
		}
	}
}
