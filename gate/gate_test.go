package gate

import (
	"errors"
	"fmt"
	"testing"

	"ontology/introspect"
)

// scripted 按调用次序返回预设结果。
type scripted struct {
	t    *testing.T
	res  []introspect.Result
	errs []error
	n    int
}

func (s *scripted) f(token string) (introspect.Result, error) {
	if s.n >= len(s.res) {
		s.t.Fatalf("unexpected upstream call #%d for %s", s.n+1, token)
	}
	r, e := s.res[s.n], s.errs[s.n]
	s.n++
	return r, e
}

var errBoom = errors.New("boom")

func activeRes(u string, iat, exp int64, scopes ...string) introspect.Result {
	return introspect.Result{Active: true, Sub: u, Iat: iat, Exp: exp, Scopes: scopes}
}

func srcName(s introspect.Source) string {
	switch s {
	case introspect.SourceCache:
		return "Cache"
	case introspect.SourceFresh:
		return "Fresh"
	case introspect.SourceStale:
		return "Stale"
	}
	return "None"
}

type step struct {
	op       string // "check" 或 "revoke"
	token    string
	need     []string
	now      int64
	sub      string
	revT     int64
	want     Verdict
	wantSrc  string
	wantMiss string
	wantCall int64
}

func runSteps(t *testing.T, g *Gate, s *scripted, steps []step) {
	t.Helper()
	for i, st := range steps {
		switch st.op {
		case "check":
			d, err := g.Check(st.token, st.need, st.now)
			if err != nil {
				t.Fatalf("step %d check(%s,%v,%d) unexpected err %v", i, st.token, st.need, st.now, err)
			}
			t.Logf("input:  Check(%s,%v,%d) | output: %s/%s%s | basis: %s",
				st.token, st.need, st.now, verdictName(d.Verdict), srcNameSafe(d), d.MissingInfo(), d.Reason)
			if d.Verdict != st.want {
				t.Fatalf("step %d verdict=%s want %s", i, verdictName(d.Verdict), verdictName(st.want))
			}
			gotSrc := "None"
			if d.HasSrc {
				gotSrc = srcName(d.Source)
			}
			if gotSrc != st.wantSrc {
				t.Fatalf("step %d source=%s want %s", i, gotSrc, st.wantSrc)
			}
			if d.Missing != st.wantMiss {
				t.Fatalf("step %d missing=%q want %q", i, d.Missing, st.wantMiss)
			}
		case "revoke":
			if err := g.Revoke(st.sub, st.revT, st.now); err != nil {
				t.Fatalf("step %d revoke(%s,%d,%d) err %v", i, st.sub, st.revT, st.now, err)
			}
			t.Logf("input:  Revoke(sub=%s,t=%d,now=%d) -> ok", st.sub, st.revT, st.now)
			continue // Revoke 不产生上游调用，不校验 wantCall
		default:
			t.Fatalf("step %d unknown op %q", i, st.op)
		}
		if g.Calls() != st.wantCall {
			t.Fatalf("step %d calls=%d want %d", i, g.Calls(), st.wantCall)
		}
	}
}

func verdictName(v Verdict) string {
	switch v {
	case Allow:
		return "Allow"
	case Inactive:
		return "Inactive"
	case Revoked:
		return "Revoked"
	case Expired:
		return "Expired"
	case Scope:
		return "Scope"
	case Unavailable:
		return "Unavailable"
	}
	return "?"
}

func srcNameSafe(d Decision) string {
	if !d.HasSrc {
		return "None"
	}
	return srcName(d.Source)
}

func mk(t *testing.T, res []introspect.Result, errs []error) (*Gate, *scripted) {
	s := &scripted{t: t, res: res, errs: errs}
	return New(10, 5, 20, s.f), s
}

func TestDecisionTable(t *testing.T) {
	t.Run("fresh boundary then stale low risk", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 100, "orders"), {}, {}},
			[]error{nil, errBoom, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 9, want: Allow, wantSrc: "Cache", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 10, want: Allow, wantSrc: "Stale", wantCall: 2},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 29, want: Allow, wantSrc: "Stale", wantCall: 3},
		})
	})

	t.Run("stale window end exclusive", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 100, "orders"), {}},
			[]error{nil, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 30, want: Unavailable, wantSrc: "None", wantCall: 2},
		})
	})

	t.Run("high risk never stale", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 100, "orders"), {}},
			[]error{nil, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:write"}, now: 10, want: Unavailable, wantSrc: "None", wantCall: 2},
		})
	})

	t.Run("risk only last segment", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 100, "orders"), {}, {}},
			[]error{nil, errBoom, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:write:x"}, now: 11, want: Allow, wantSrc: "Stale", wantCall: 2},
			{op: "check", token: "t1", need: []string{"orders:write"}, now: 12, want: Unavailable, wantSrc: "None", wantCall: 3},
		})
	})

	t.Run("exp caps u and stale window absent", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 8, "orders"), {}},
			[]error{nil, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 7, want: Allow, wantSrc: "Cache", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 8, want: Unavailable, wantSrc: "None", wantCall: 2},
		})
	})

	t.Run("negative record no stale", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{{Active: false}, {}},
			[]error{nil, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t2", need: []string{"orders:read"}, now: 0, want: Inactive, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t2", need: []string{"orders:read"}, now: 4, want: Inactive, wantSrc: "Cache", wantCall: 1},
			{op: "check", token: "t2", need: []string{"orders:read"}, now: 5, want: Unavailable, wantSrc: "None", wantCall: 2},
		})
	})

	t.Run("revoke cached instant no upstream", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 100, "orders")},
			[]error{nil})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "revoke", sub: "u1", revT: 0, now: 11},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 12, want: Revoked, wantSrc: "Cache", wantCall: 1},
		})
	})

	t.Run("revoke equality nb and nb+1", func(t *testing.T) {
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 5, 100, "orders"), activeRes("u1", 6, 100, "orders")},
			[]error{nil, nil})
		runSteps(t, g, s, []step{
			{op: "revoke", sub: "u1", revT: 5, now: 0},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 1, want: Revoked, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t2", need: []string{"orders:read"}, now: 2, want: Allow, wantSrc: "Fresh", wantCall: 2},
		})
	})

	t.Run("inactive has highest priority", func(t *testing.T) {
		g, s := mk(t, []introspect.Result{{Active: false}}, []error{nil})
		runSteps(t, g, s, []step{
			{op: "revoke", sub: "u1", revT: 100, now: 0},
			{op: "check", token: "t", need: []string{"orders"}, now: 1, want: Inactive, wantSrc: "Fresh", wantCall: 1},
		})
	})

	t.Run("revoked before expired and scope", func(t *testing.T) {
		g, s := mk(t, []introspect.Result{activeRes("u1", 0, 100, "orders")}, []error{nil})
		runSteps(t, g, s, []step{
			{op: "revoke", sub: "u1", revT: 0, now: 0},
			{op: "check", token: "t", need: []string{"billing"}, now: 10, want: Revoked, wantSrc: "Fresh", wantCall: 1},
		})
	})

	t.Run("expired before scope", func(t *testing.T) {
		g, s := mk(t, []introspect.Result{activeRes("u1", 0, 5, "orders")}, []error{nil})
		runSteps(t, g, s, []step{
			{op: "check", token: "t", need: []string{"billing"}, now: 10, want: Expired, wantSrc: "Fresh", wantCall: 1},
		})
	})

	t.Run("scope first missing among multiple", func(t *testing.T) {
		g, s := mk(t, []introspect.Result{activeRes("u1", 0, 100, "orders")}, []error{nil})
		runSteps(t, g, s, []step{
			{op: "check", token: "t", need: []string{"orders:read", "billing:read"}, now: 0, want: Scope, wantSrc: "Fresh", wantMiss: "billing:read", wantCall: 1},
		})
	})

	t.Run("scope coverage downward table", func(t *testing.T) {
		g, s := mk(t, []introspect.Result{
			activeRes("u1", 0, 100, "orders:read"),
			activeRes("u2", 0, 100, "orders"),
			activeRes("u2", 0, 100, "orders"),
			activeRes("u2", 0, 100, "orders"),
		}, []error{nil, nil, nil, nil})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders"}, now: 0, want: Scope, wantSrc: "Fresh", wantMiss: "orders", wantCall: 1},
			{op: "check", token: "t2", need: []string{"orders:read"}, now: 1, want: Allow, wantSrc: "Fresh", wantCall: 2},
			{op: "check", token: "t2", need: []string{"orders:read:x"}, now: 2, want: Allow, wantSrc: "Cache", wantCall: 2},
			{op: "check", token: "t2", need: []string{"ordersx"}, now: 3, want: Scope, wantSrc: "Cache", wantMiss: "ordersx", wantCall: 2},
		})
	})

	t.Run("failure does not extend freshness", func(t *testing.T) {
		// now=10 失败后记录 f0 仍为 0；now=30 已在窗外，必然 Unavailable。
		g, s := mk(t,
			[]introspect.Result{activeRes("u1", 0, 100, "orders"), {}, {}},
			[]error{nil, errBoom, errBoom})
		runSteps(t, g, s, []step{
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 0, want: Allow, wantSrc: "Fresh", wantCall: 1},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 10, want: Allow, wantSrc: "Stale", wantCall: 2},
			{op: "check", token: "t1", need: []string{"orders:read"}, now: 30, want: Unavailable, wantSrc: "None", wantCall: 3},
		})
	})

	t.Run("revoke before cache", func(t *testing.T) {
		g, s := mk(t, []introspect.Result{activeRes("u9", 5, 100)}, []error{nil})
		runSteps(t, g, s, []step{
			{op: "revoke", sub: "u9", revT: 5, now: 0},
			{op: "check", token: "t", need: []string{"orders"}, now: 1, want: Revoked, wantSrc: "Fresh", wantCall: 1},
		})
	})
}

func TestRejectionsChangeNothing(t *testing.T) {
	g, _ := mk(t, []introspect.Result{activeRes("u1", 0, 100, "orders")}, []error{nil})
	reject := func(name string, want error, fn func() error) {
		t.Helper()
		if err := fn(); !errors.Is(err, want) {
			t.Fatalf("%s: err=%v want %v", name, err, want)
		}
	}
	reject("empty token", ErrInvalid, func() error { _, e := g.Check("", []string{"a"}, -1); return e })
	reject("nil need", ErrInvalid, func() error { _, e := g.Check("t", nil, 0); return e })
	reject("empty need item", ErrInvalid, func() error { _, e := g.Check("t", []string{"a", ""}, 0); return e })
	reject("revoke empty sub", ErrInvalid, func() error { return g.Revoke("", 0, -1) })
	reject("revoke t negative", ErrInvalid, func() error { return g.Revoke("u", -1, 0) })
	reject("revoke t too big", ErrInvalid, func() error { return g.Revoke("u", 1_000_000_000_000_001, 0) })
	reject("now negative", ErrTime, func() error { _, e := g.Check("t", []string{"a"}, -1); return e })
	reject("now too big", ErrTime, func() error { _, e := g.Check("t", []string{"a"}, 1_000_000_000_000_001); return e })

	d, err := g.Check("t", []string{"orders:read"}, 50)
	if err != nil || d.Verdict != Allow {
		t.Fatalf("seed: %+v err=%v", d, err)
	}
	// 参数非法仍优先于时钟回退
	reject("invalid args beat clock", ErrInvalid, func() error { _, e := g.Check("", []string{"a"}, 10); return e })
	reject("clock backwards", ErrClock, func() error { _, e := g.Check("t", []string{"orders:read"}, 49); return e })
	// 被拒绝调用不增加 Calls、不增缓存条目
	if g.Calls() != 1 {
		t.Fatalf("calls=%d want 1", g.Calls())
	}
	if g.CachedTokens() != 1 {
		t.Fatalf("tokens=%d want 1", g.CachedTokens())
	}
	// Unavailable 也推进水位：now=51 失败后 51 可用、50 回退
	g2, _ := mk(t, []introspect.Result{{}}, []error{errBoom})
	if d, err := g2.Check("x", []string{"orders:read"}, 51); err != nil || d.Verdict != Unavailable {
		t.Fatalf("unavailable: %+v err=%v", d, err)
	}
	reject("after unavailable clock back", ErrClock, func() error { _, e := g2.Check("x", []string{"orders:read"}, 50); return e })
}

func TestRevokeTouchesNoCacheEntries(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("%d tokens", n), func(t *testing.T) {
			res := make([]introspect.Result, n)
			errs := make([]error, n)
			for i := 0; i < n; i++ {
				res[i] = activeRes("shared", 0, 100, "orders")
			}
			g, _ := mk(t, res, errs)
			for i := 0; i < n; i++ {
				tok := fmt.Sprintf("tok-%d", i)
				if d, err := g.Check(tok, []string{"orders:read"}, 0); err != nil || d.Verdict != Allow {
					t.Fatalf("seed %d: %+v err=%v", i, d, err)
				}
			}
			if g.CachedTokens() != n || g.Calls() != int64(n) {
				t.Fatalf("seed: tokens=%d calls=%d", g.CachedTokens(), g.Calls())
			}
			if err := g.Revoke("shared", 0, 1); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			if got := g.RevokeCacheTouches(); got != 0 {
				t.Fatalf("revoke touched %d cache entries, want 0", got)
			}
			if g.CachedTokens() != n {
				t.Fatalf("revoke changed cache size: %d want %d", g.CachedTokens(), n)
			}
			// 撤销后所有缓存令牌立即 Revoked，且不触发任何上游调用
			d, err := g.Check("tok-0", []string{"orders:read"}, 2)
			if err != nil || d.Verdict != Revoked {
				t.Fatalf("post-revoke: %+v err=%v", d, err)
			}
			if g.Calls() != int64(n) {
				t.Fatalf("revoked cache hit triggered upstream: calls=%d", g.Calls())
			}
		})
	}
}
