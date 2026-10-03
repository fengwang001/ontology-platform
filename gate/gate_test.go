package gate_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/gate"
	"ontology/introspect"
	"ontology/scope"
)

type upstream struct {
	mu       sync.Mutex
	results  map[string]introspect.Result
	failures map[string]int
	calls    []string
	attempts map[string]int
	block    map[string]chan struct{}
	entered  map[string]chan struct{}
}

func newUpstream() *upstream {
	return &upstream{
		results:  map[string]introspect.Result{},
		failures: map[string]int{},
		attempts: map[string]int{},
		block:    map[string]chan struct{}{},
		entered:  map[string]chan struct{}{},
	}
}

func (u *upstream) call(token string) (introspect.Result, error) {
	u.mu.Lock()
	u.calls = append(u.calls, token)
	u.attempts[token]++
	failures := 0
	if u.attempts[token] > 1 {
		failures = u.failures[token]
	}
	if failures > 0 {
		u.failures[token]--
	}
	result := u.results[token]
	release, blocking := u.block[token]
	entered := u.entered[token]
	u.mu.Unlock()
	if blocking {
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		<-release
	}
	if failures > 0 {
		return introspect.Result{}, errors.New("upstream unavailable")
	}
	return result, nil
}

func activeResult(sub string, iat, exp int64, scopes ...string) introspect.Result {
	return introspect.Result{Active: true, Sub: sub, Iat: iat, Exp: exp, Scopes: scopes}
}

func mustCheck(t *testing.T, g *gate.Gateway, token string, need []string, now, calls int64) gate.Decision {
	t.Helper()
	decision, err := g.Check(token, need, now)
	if err != nil {
		t.Fatalf("Check(%q,%v,%d) error: %v", token, need, now, err)
	}
	if got := g.Calls(); got != calls {
		t.Fatalf("Check(%q,%v,%d) calls=%d want %d", token, need, now, got, calls)
	}
	t.Logf("input Check token=%q need=%v now=%d; output verdict=%s source=%s missing=%q; basis=%s; calls=%d",
		token, need, now, decision.Verdict, decision.Source, decision.Missing, decision.Reason, calls)
	return decision
}

func assertDecision(t *testing.T, got gate.Decision, verdict gate.Verdict, source gate.Source, missing string) {
	t.Helper()
	if got.Verdict != verdict || got.Source != source || got.Missing != missing {
		t.Fatalf("decision=%+v want verdict=%s source=%s missing=%q", got, verdict, source, missing)
	}
}

func newGateway(t *testing.T, up *upstream, positive, negative, grace int64) *gate.Gateway {
	t.Helper()
	g, err := gate.New(positive, negative, grace, up.call)
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	return g
}

func TestScopeRules(t *testing.T) {
	cases := []struct {
		granted []string
		need    []string
		missing string
		has     bool
		high    bool
	}{
		{[]string{"orders"}, []string{"orders:read"}, "", false, false},
		{[]string{"orders"}, []string{"orders:read:x"}, "", false, false},
		{[]string{"orders"}, []string{"ordersx"}, "ordersx", true, false},
		{[]string{"orders:read"}, []string{"orders"}, "orders", true, false},
		{[]string{"orders"}, []string{"orders:write"}, "", false, true},
		{[]string{"orders"}, []string{"orders:x:admin"}, "", false, true},
		{[]string{"orders"}, []string{"orders:write:x"}, "", false, false},
		{[]string{"orders"}, []string{"orders:read", "billing:read"}, "billing:read", true, false},
	}
	for index, tc := range cases {
		t.Run(fmt.Sprintf("%d", index), func(t *testing.T) {
			missing, ok := scope.FirstMissing(tc.granted, tc.need)
			if missing != tc.missing || ok != tc.has || scope.High(tc.need) != tc.high {
				t.Fatalf("got missing=%q ok=%v high=%v", missing, ok, scope.High(tc.need))
			}
		})
	}
}

func TestGatewayTable(t *testing.T) {
	t.Run("fresh fails exactly at u", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 100, "orders")
		g := newGateway(t, up, 10, 5, 20)
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 0, 1), gate.Allow, gate.Fresh, "")
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 9, 1), gate.Allow, gate.Cache, "")
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 10, 2), gate.Allow, gate.Fresh, "")
	})

	t.Run("stale starts exactly at u", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 100, "orders")
		up.failures["t"] = 1
		g := newGateway(t, up, 10, 5, 20)
		mustCheck(t, g, "t", []string{"orders:read"}, 0, 1)
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 10, 2), gate.Allow, gate.Stale, "")
	})

	t.Run("stale ends exactly at u plus grace", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 100, "orders")
		up.failures["t"] = 1
		g := newGateway(t, up, 10, 5, 20)
		mustCheck(t, g, "t", []string{"orders:read"}, 0, 1)
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 30, 2), gate.Unavailable, gate.None, "")
	})

	t.Run("high and low risk differ under same failure", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			need    []string
			verdict gate.Verdict
			source  gate.Source
		}{
			{"low", []string{"orders:read"}, gate.Allow, gate.Stale},
			{"high", []string{"orders:write"}, gate.Unavailable, gate.None},
		} {
			t.Run(tc.name, func(t *testing.T) {
				up := newUpstream()
				up.results["t"] = activeResult("u", 0, 100, "orders")
				up.failures["t"] = 1
				g := newGateway(t, up, 10, 5, 20)
				mustCheck(t, g, "t", tc.need, 0, 1)
				assertDecision(t, mustCheck(t, g, "t", tc.need, 10, 2), tc.verdict, tc.source, "")
			})
		}
	})

	t.Run("u capped at exp has no stale period", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 8, "orders")
		up.failures["t"] = 1
		g := newGateway(t, up, 10, 5, 20)
		mustCheck(t, g, "t", []string{"orders:read"}, 0, 1)
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 8, 2), gate.Unavailable, gate.None, "")
	})

	t.Run("negative expires and is not reused", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = introspect.Result{}
		up.failures["t"] = 1
		g := newGateway(t, up, 10, 5, 20)
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 0, 1), gate.Inactive, gate.Fresh, "")
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 4, 1), gate.Inactive, gate.Cache, "")
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 5, 2), gate.Unavailable, gate.None, "")
	})

	t.Run("failure does not extend freshness", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 100, "orders")
		up.failures["t"] = 2
		g := newGateway(t, up, 10, 5, 20)
		mustCheck(t, g, "t", []string{"orders:read"}, 0, 1)
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 10, 2), gate.Allow, gate.Stale, "")
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 11, 3), gate.Allow, gate.Stale, "")
	})
}

func TestRevocationAndRejection(t *testing.T) {
	t.Run("iat equal and greater than watermark", func(t *testing.T) {
		for _, iat := range []int64{5, 6} {
			up := newUpstream()
			up.results["t"] = activeResult("u", iat, 100, "orders")
			g := newGateway(t, up, 10, 5, 20)
			if err := g.Revoke("u", 5, 0); err != nil {
				t.Fatal(err)
			}
			want := gate.Allow
			if iat == 5 {
				want = gate.Revoked
			}
			assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 1, 1), want, gate.Fresh, "")
		}
	})

	t.Run("cached revocation is immediate", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 100, "orders")
		g := newGateway(t, up, 100, 5, 20)
		mustCheck(t, g, "t", []string{"orders:read"}, 0, 1)
		if err := g.Revoke("u", 0, 1); err != nil {
			t.Fatal(err)
		}
		assertDecision(t, mustCheck(t, g, "t", []string{"orders:read"}, 2, 1), gate.Revoked, gate.Cache, "")
	})

	t.Run("same subject newer iat remains allowed", func(t *testing.T) {
		up := newUpstream()
		up.results["old"] = activeResult("u", 0, 100, "orders")
		up.results["new"] = activeResult("u", 1, 100, "orders")
		g := newGateway(t, up, 100, 5, 20)
		mustCheck(t, g, "old", []string{"orders:read"}, 0, 1)
		if err := g.Revoke("u", 0, 1); err != nil {
			t.Fatal(err)
		}
		assertDecision(t, mustCheck(t, g, "old", []string{"orders:read"}, 2, 1), gate.Revoked, gate.Cache, "")
		assertDecision(t, mustCheck(t, g, "new", []string{"orders:read"}, 3, 2), gate.Allow, gate.Fresh, "")
	})

	t.Run("rejected calls do not change calls or clock", func(t *testing.T) {
		up := newUpstream()
		up.results["t"] = activeResult("u", 0, 100, "orders")
		g := newGateway(t, up, 10, 5, 20)
		mustCheck(t, g, "t", []string{"orders"}, 8, 1)
		invalid := []struct {
			token string
			need  []string
			now   int64
			want  error
		}{
			{"", []string{"orders"}, 9, gate.ErrInvalidArgument},
			{"t", nil, 9, gate.ErrInvalidArgument},
			{"t", []string{""}, 9, gate.ErrInvalidArgument},
			{"t", []string{"orders"}, -1, gate.ErrInvalidTime},
			{"t", []string{"orders"}, 7, gate.ErrClockRollback},
		}
		for _, in := range invalid {
			_, err := g.Check(in.token, in.need, in.now)
			if !errors.Is(err, in.want) {
				t.Fatalf("%+v got %v", in, err)
			}
		}
		if err := g.Revoke("", 0, 9); !errors.Is(err, gate.ErrInvalidArgument) {
			t.Fatalf("revoke empty sub got %v", err)
		}
		if err := g.Revoke("u", -1, 9); !errors.Is(err, gate.ErrInvalidArgument) {
			t.Fatalf("revoke bad t got %v", err)
		}
		if err := g.Revoke("u", 0, 7); !errors.Is(err, gate.ErrClockRollback) {
			t.Fatalf("revoke rollback got %v", err)
		}
		assertDecision(t, mustCheck(t, g, "t", []string{"orders"}, 9, 1), gate.Allow, gate.Cache, "")
	})
}

func TestConcurrentMissSingleFlight(t *testing.T) {
	up := newUpstream()
	up.results["t"] = activeResult("u", 0, 100, "orders")
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	up.block["t"] = release
	up.entered["t"] = entered
	g := newGateway(t, up, 10, 5, 20)

	decisions := make(chan gate.Decision, 2)
	go func() {
		d, _ := g.Check("t", []string{"orders:read"}, 0)
		decisions <- d
	}()
	<-entered
	go func() {
		d, _ := g.Check("t", []string{"orders:read"}, 0)
		decisions <- d
	}()
	time.Sleep(10 * time.Millisecond)
	close(release)

	for i := 0; i < 2; i++ {
		assertDecision(t, <-decisions, gate.Allow, gate.Fresh, "")
	}
	if calls := g.Calls(); calls != 1 {
		t.Fatalf("calls=%d want 1", calls)
	}
}

func TestConcurrentFailureWaitersUseOwnContext(t *testing.T) {
	up := newUpstream()
	up.results["t"] = activeResult("u", 0, 100, "orders")
	up.failures["t"] = 1
	g := newGateway(t, up, 10, 5, 20)
	mustCheck(t, g, "t", []string{"orders:read"}, 0, 1)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	up.block["t"] = release
	up.entered["t"] = entered
	decisions := make(chan gate.Decision, 3)
	go func() {
		d, _ := g.Check("t", []string{"orders:read"}, 10)
		decisions <- d
	}()
	<-entered
	go func() {
		d, _ := g.Check("t", []string{"orders:read"}, 29)
		decisions <- d
	}()
	go func() {
		d, _ := g.Check("t", []string{"orders:admin"}, 29)
		decisions <- d
	}()
	time.Sleep(10 * time.Millisecond)
	close(release)

	want := map[gate.Verdict]gate.Source{
		gate.Allow:       gate.Stale,
		gate.Unavailable: gate.None,
	}
	for i := 0; i < 3; i++ {
		got := <-decisions
		if source, ok := want[got.Verdict]; !ok || got.Source != source {
			t.Fatalf("unexpected decision: %+v", got)
		}
	}
	if calls := g.Calls(); calls != 2 {
		t.Fatalf("calls=%d want 2", calls)
	}
}

func TestRevokeTouchesNoCachedEntries(t *testing.T) {
	for _, size := range []int{100, 10000} {
		t.Run(fmt.Sprintf("%d", size), func(t *testing.T) {
			up := newUpstream()
			g := newGateway(t, up, 1_000_000_000, 1_000_000_000, 1_000_000_000)
			for i := 0; i < size; i++ {
				token := fmt.Sprintf("token-%d", i)
				up.results[token] = activeResult("u", 0, 100_000_000_000_000, "orders")
				mustCheck(t, g, token, []string{"orders"}, int64(i), int64(i+1))
			}
			if err := g.Revoke("u", 0, int64(size)); err != nil {
				t.Fatal(err)
			}
			if g.Len() != size || g.RevokeTouchedEntries() != 0 {
				t.Fatalf("len=%d touched=%d", g.Len(), g.RevokeTouchedEntries())
			}
		})
	}
}
