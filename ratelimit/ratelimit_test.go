package ratelimit_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/ratelimit"
)

func mustNew(t *testing.T, capacity int64) *ratelimit.Limiter {
	t.Helper()
	l, err := ratelimit.New(capacity)
	if err != nil {
		t.Fatalf("New(%d): %v", capacity, err)
	}
	return l
}

func mustAddTier(t *testing.T, l *ratelimit.Limiter, name string, rank int, tt, bb int64) {
	t.Helper()
	if err := l.AddTier(name, rank, tt, bb); err != nil {
		t.Fatalf("AddTier(%q): %v", name, err)
	}
}

func mustAddRoute(t *testing.T, l *ratelimit.Limiter, path, scope string, cost int64) {
	t.Helper()
	if err := l.AddRoute(path, scope, cost); err != nil {
		t.Fatalf("AddRoute(%q): %v", path, err)
	}
}

// stdLimiter：free(rank 1, T=1000, B=2)、pro(rank 2, T=100, B=5)、tiny(rank 3, T=1, B=10)。
// 故意先登记 pro 再登记 free，验证最小 rank 选择不依赖插入顺序。
func stdLimiter(t *testing.T, capacity int64) *ratelimit.Limiter {
	t.Helper()
	l := mustNew(t, capacity)
	mustAddTier(t, l, "pro", 2, 100, 5)
	mustAddTier(t, l, "free", 1, 1000, 2)
	mustAddTier(t, l, "tiny", 3, 1, 10)
	mustAddRoute(t, l, "/orders", "orders", 1)
	mustAddRoute(t, l, "/bulk", "bulk", 3)
	mustAddRoute(t, l, "/public", "", 1)
	mustAddRoute(t, l, "/tiny", "", 1)
	mustAddRoute(t, l, "/ten", "", 10)
	mustAddRoute(t, l, "/eleven", "", 11)
	return l
}

type step struct {
	sub        string
	path       string
	scopes     []string
	now        int64
	wantErr    error
	wantResult ratelimit.Result
}

func TestAllowSequences(t *testing.T) {
	free := []string{"orders", "tier:free"}
	cases := []struct {
		name     string
		capacity int64
		steps    []step
	}{
		{
			name:     "spec walkthrough",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/orders", scopes: free, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u", path: "/orders", scopes: free, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
				{sub: "u", path: "/orders", scopes: free, now: 0, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 2, Remaining: 0, Reset: 2, RetryAfter: 1, Tier: "free"}},
				{sub: "u", path: "/orders", scopes: []string{"orders", "tier:free", "tier:pro"}, now: 0, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 5, Remaining: 0, Reset: 2, RetryAfter: 2, Tier: "pro"}},
				{sub: "u", path: "/orders", scopes: []string{"orders", "tier:free", "tier:pro"}, now: 1600, wantResult: ratelimit.Result{Allowed: true, Limit: 5, Remaining: 0, Reset: 1, Tier: "pro"}},
			},
		},
		{
			name:     "new-now exactly B*T allowed, B*T+1 limited",
			capacity: 16,
			steps: []step{
				// tiny: T=1, B=10。第 10 次 new-now=10=B*T 恰放行，第 11 次大 1 被限流。
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 9, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 8, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 7, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 6, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 5, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 4, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 3, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 2, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 1, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 0, Reset: 1, Tier: "tiny"}},
				{sub: "u", path: "/tiny", scopes: []string{"tier:tiny"}, now: 0, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 10, Remaining: 0, Reset: 1, RetryAfter: 1, Tier: "tiny"}},
			},
		},
		{
			name:     "cost equals B allowed, B+1 impossible",
			capacity: 16,
			steps: []step{
				// tiny: T=1, B=10。cost=10==B 恰可放行；cost=11==B+1 永远无法满足（不带响应头）。
				{sub: "u1", path: "/ten", scopes: []string{"tier:tiny"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 10, Remaining: 0, Reset: 1, Tier: "tiny"}},
				{sub: "u2", path: "/eleven", scopes: []string{"tier:tiny"}, now: 0, wantErr: ratelimit.ErrImpossible},
			},
		},
		{
			name:     "remaining floors down",
			capacity: 16,
			steps: []step{
				// pro: T=100, B=5。new-now=190 时 (500-190)/100=3.1 向下取整为 3。
				{sub: "u", path: "/public", scopes: []string{"tier:pro"}, now: 50, wantResult: ratelimit.Result{Allowed: true, Limit: 5, Remaining: 4, Reset: 1, Tier: "pro"}},
				{sub: "u", path: "/public", scopes: []string{"tier:pro"}, now: 60, wantResult: ratelimit.Result{Allowed: true, Limit: 5, Remaining: 3, Reset: 1, Tier: "pro"}},
			},
		},
		{
			name:     "reset exact second does not round up",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				// new-now=2000 毫秒，Reset 恰为 2 秒而非 3。
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
			},
		},
		{
			name:     "idle TAT below now",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				// TAT=1000<now=5000，a 取 now。
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 5000, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
			},
		},
		{
			name:     "unregistered tier scope ignored",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/orders", scopes: []string{"orders", "tier:platinum"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
			},
		},
		{
			name:     "no tier scope falls back to min rank",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/orders", scopes: []string{"orders"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
			},
		},
		{
			name:     "forbidden precedes impossible and neither touches TAT",
			capacity: 16,
			steps: []step{
				// /bulk cost=3>B=2，但无 bulk scope 时先报无权限。
				{sub: "u", path: "/bulk", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrForbidden},
				{sub: "u", path: "/bulk", scopes: []string{"bulk", "tier:free"}, now: 0, wantErr: ratelimit.ErrImpossible},
				// 两次拒绝均未触碰 TAT：仍是首次放行。
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
			},
		},
		{
			name:     "limited rejection keeps TAT",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 2, Remaining: 0, Reset: 2, RetryAfter: 1, Tier: "free"}},
				// 若 TAT 被限流改写为 3000，此处 new-now=3000>2000 会被限流；放行证明 TAT 仍为 2000。
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 1000, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
			},
		},
		{
			name:     "clock regression and limited does not advance max now",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 500, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 500, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 499, wantErr: ratelimit.ErrClockRegression},
				// TAT=2500，new-now=2500>2000 被限流；最大 now 不得推进到 1000。
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 1000, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 2, Remaining: 0, Reset: 2, RetryAfter: 1, Tier: "free"}},
				// now=700<1000 但不小于已放行的最大 now=500：不是时钟回退，仍按限流处理。
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 700, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 2, Remaining: 0, Reset: 2, RetryAfter: 1, Tier: "free"}},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 1500, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 1400, wantErr: ratelimit.ErrClockRegression},
			},
		},
		{
			name:     "same-now versus increasing-now TAT difference",
			capacity: 16,
			steps: []step{
				// a 同一时刻连发：TAT 累积到 2000，第三次被限流。
				{sub: "a", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "a", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
				{sub: "a", path: "/public", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrLimited, wantResult: ratelimit.Result{Limit: 2, Remaining: 0, Reset: 2, RetryAfter: 1, Tier: "free"}},
				// b 递增时刻：a 取 now，TAT 不累积，三次均放行。
				{sub: "b", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "b", path: "/public", scopes: []string{"tier:free"}, now: 1500, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "b", path: "/public", scopes: []string{"tier:free"}, now: 1500, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
			},
		},
		{
			name:     "two subjects are independent",
			capacity: 16,
			steps: []step{
				{sub: "u1", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u2", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u1", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
				{sub: "u2", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 0, Reset: 2, Tier: "free"}},
			},
		},
		{
			name:     "subject table full then released as now advances",
			capacity: 1,
			steps: []step{
				{sub: "u1", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u2", path: "/public", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrSubjectTableFull},
				{sub: "u2", path: "/public", scopes: []string{"tier:free"}, now: 999, wantErr: ratelimit.ErrSubjectTableFull},
				// u1 的 TAT=1000<=now，不再占用，u2 放行。
				{sub: "u2", path: "/public", scopes: []string{"tier:free"}, now: 1000, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
			},
		},
		{
			name:     "forbidden and impossible do not occupy subject table",
			capacity: 1,
			steps: []step{
				{sub: "u1", path: "/orders", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrForbidden},
				{sub: "u1", path: "/bulk", scopes: []string{"bulk", "tier:free"}, now: 0, wantErr: ratelimit.ErrImpossible},
				{sub: "u2", path: "/public", scopes: []string{"tier:free"}, now: 0, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
				{sub: "u3", path: "/public", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrSubjectTableFull},
			},
		},
		{
			name:     "invalid arguments",
			capacity: 16,
			steps: []step{
				{sub: "", path: "/public", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrInvalidArgument},
				{sub: "u", path: "", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrInvalidArgument},
				{sub: "u", path: "/public", scopes: []string{"tier:free", ""}, now: 0, wantErr: ratelimit.ErrInvalidArgument},
				// 参数非法优先于时间非法。
				{sub: "", path: "/public", scopes: nil, now: -1, wantErr: ratelimit.ErrInvalidArgument},
			},
		},
		{
			name:     "invalid time",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: -1, wantErr: ratelimit.ErrInvalidTime},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 1_000_000_000_000_001, wantErr: ratelimit.ErrInvalidTime},
				{sub: "u", path: "/public", scopes: []string{"tier:free"}, now: 1_000_000_000_000_000, wantResult: ratelimit.Result{Allowed: true, Limit: 2, Remaining: 1, Reset: 1, Tier: "free"}},
			},
		},
		{
			name:     "route not found",
			capacity: 16,
			steps: []step{
				{sub: "u", path: "/nope", scopes: []string{"tier:free"}, now: 0, wantErr: ratelimit.ErrRouteNotFound},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := stdLimiter(t, c.capacity)
			for i, s := range c.steps {
				res, err := l.Allow(s.sub, s.path, s.scopes, s.now)
				if !errors.Is(err, s.wantErr) {
					t.Fatalf("step %d: err=%v, want %v", i, err, s.wantErr)
				}
				if res != s.wantResult {
					t.Fatalf("step %d: result=%+v, want %+v", i, res, s.wantResult)
				}
			}
		})
	}
}

func TestNoTiers(t *testing.T) {
	l := mustNew(t, 16)
	mustAddRoute(t, l, "/public", "", 1)
	res, err := l.Allow("u", "/public", []string{"tier:free"}, 0)
	if !errors.Is(err, ratelimit.ErrNoTier) {
		t.Fatalf("err=%v, want %v", err, ratelimit.ErrNoTier)
	}
	if res != (ratelimit.Result{}) {
		t.Fatalf("result=%+v, want zero (no headers)", res)
	}
}

func TestNewValidation(t *testing.T) {
	for _, capacity := range []int64{0, -1, 1_000_001} {
		if _, err := ratelimit.New(capacity); !errors.Is(err, ratelimit.ErrInvalidArgument) {
			t.Errorf("New(%d): err=%v, want %v", capacity, err, ratelimit.ErrInvalidArgument)
		}
	}
	for _, capacity := range []int64{1, 1_000_000} {
		if _, err := ratelimit.New(capacity); err != nil {
			t.Errorf("New(%d): err=%v, want nil", capacity, err)
		}
	}
}

func TestAddTierValidation(t *testing.T) {
	cases := []struct {
		name     string
		tierName string
		rank     int
		tt, bb   int64
		wantErr  error
	}{
		{"ok", "free", 1, 1000, 2, nil},
		{"empty name", "", 2, 1000, 2, ratelimit.ErrInvalidArgument},
		{"rank zero", "pro", 0, 1000, 2, ratelimit.ErrInvalidArgument},
		{"rank too big", "pro", 1001, 1000, 2, ratelimit.ErrInvalidArgument},
		{"T zero", "pro", 2, 0, 2, ratelimit.ErrInvalidArgument},
		{"T too big", "pro", 2, 1_000_001, 2, ratelimit.ErrInvalidArgument},
		{"B zero", "pro", 2, 1000, 0, ratelimit.ErrInvalidArgument},
		{"B too big", "pro", 2, 1000, 1_000_001, ratelimit.ErrInvalidArgument},
		{"dup name", "free", 3, 100, 5, ratelimit.ErrDuplicate},
		{"dup rank", "pro", 1, 100, 5, ratelimit.ErrDuplicate},
		{"invalid precedes duplicate", "free", 0, 1000, 2, ratelimit.ErrInvalidArgument},
		{"ok after dups", "pro", 2, 100, 5, nil},
	}
	l := mustNew(t, 16)
	for _, c := range cases {
		err := l.AddTier(c.tierName, c.rank, c.tt, c.bb)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: AddTier(%q,%d) err=%v, want %v", c.name, c.tierName, c.rank, err, c.wantErr)
		}
	}
}

func TestAddRouteValidation(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		scope   string
		cost    int64
		wantErr error
	}{
		{"ok", "/a", "", 1, nil},
		{"empty path", "", "", 1, ratelimit.ErrInvalidArgument},
		{"cost zero", "/b", "", 0, ratelimit.ErrInvalidArgument},
		{"cost too big", "/b", "", 1_000_001, ratelimit.ErrInvalidArgument},
		{"dup path", "/a", "", 1, ratelimit.ErrDuplicate},
		{"invalid precedes duplicate", "", "", 0, ratelimit.ErrInvalidArgument},
		{"ok with scope", "/b", "orders", 1_000_000, nil},
	}
	l := mustNew(t, 16)
	for _, c := range cases {
		err := l.AddRoute(c.path, c.scope, c.cost)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: AddRoute(%q) err=%v, want %v", c.name, c.path, err, c.wantErr)
		}
	}
}

// TestConcurrency：N 个主体各自连发 3 次（now 相同），无论并发交错如何，
// 每个主体恰放行 2 次、被限流 1 次，总计放行 2N、限流 N。
func TestConcurrency(t *testing.T) {
	const n = 64
	l := stdLimiter(t, n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed, limited := 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sub := string(rune('a'+i/26)) + string(rune('a'+i%26))
			for j := 0; j < 3; j++ {
				_, err := l.Allow(sub, "/public", []string{"tier:free"}, 0)
				mu.Lock()
				switch {
				case err == nil:
					allowed++
				case errors.Is(err, ratelimit.ErrLimited):
					limited++
				default:
					t.Errorf("unexpected err: %v", err)
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if allowed != 2*n || limited != n {
		t.Fatalf("allowed=%d limited=%d, want %d/%d", allowed, limited, 2*n, n)
	}
}
