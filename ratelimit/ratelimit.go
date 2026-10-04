// Package ratelimit 组合作用域鉴权、等级推导与 GCRA 限流，
// 并生成 Limit/Remaining/Reset/Retry-After 响应头。
package ratelimit

import (
	"errors"
	"sync"

	"ontology/gcra"
	"ontology/tier"
)

// MaxNow 是 now 的最大合法值（10^15 毫秒）。
const MaxNow = int64(1_000_000_000_000_000)

var (
	ErrInvalidParam     = errors.New("ratelimit: invalid parameter")
	ErrInvalidTime      = errors.New("ratelimit: now out of range [0,1e15]")
	ErrClockRollback    = errors.New("ratelimit: clock rollback")
	ErrRouteNotFound    = errors.New("ratelimit: route not found")
	ErrNoTier           = errors.New("ratelimit: no tier registered")
	ErrForbidden        = errors.New("ratelimit: missing required scope")
	ErrNeverSatisfiable = errors.New("ratelimit: cost exceeds burst, can never be satisfied")
	ErrTableFull        = errors.New("ratelimit: subject table full")
	ErrLimited          = errors.New("ratelimit: rate limited")
	ErrDuplicateRoute   = errors.New("ratelimit: route already exists")
)

// Result 是 Allow 的结果。仅被限流时错误与头一并返回（err == ErrLimited）；
// 其余错误返回零值 Result。
type Result struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	Reset      int64 // 秒
	RetryAfter int64 // 秒，仅被限流时有效
	Tier       string
}

type route struct {
	scope string
	cost  int64
}

// Limiter 是网关限流器，全部方法可并发调用，
// 并发结果等价于某个串行顺序。
type Limiter struct {
	mu     sync.Mutex
	tiers  *tier.Set
	routes map[string]route
	tats   map[string]int64
	maxNow int64 // 已放行请求见过的最大 now，初值 0
	cap    int
}

// New 构造限流器，capacity 为主体表容量 S（1 到 10^6）。
func New(capacity int) (*Limiter, error) {
	if capacity < 1 || capacity > 1_000_000 {
		return nil, ErrInvalidParam
	}
	return &Limiter{
		tiers:  tier.NewSet(),
		routes: make(map[string]route),
		tats:   make(map[string]int64),
		cap:    capacity,
	}, nil
}

// AddTier 登记等级，语义见 tier.Set.Add。
func (l *Limiter) AddTier(name string, rank int, T, B int64) error {
	return l.tiers.Add(name, rank, T, B)
}

// AddRoute 登记路由。path 非空且唯一（精确匹配）；scope 可为空串表示公开；
// cost ∈ [1,1e6]。已登记的路由不可修改或删除。
// 拒绝顺序：参数非法 > 重复。
func (l *Limiter) AddRoute(path, scope string, cost int64) error {
	if path == "" || cost < 1 || cost > 1_000_000 {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.routes[path]; ok {
		return ErrDuplicateRoute
	}
	l.routes[path] = route{scope: scope, cost: cost}
	return nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// Allow 判定一次请求。拒绝顺序：参数非法 > 时间非法 > 时钟回退 >
// 路由不存在 > 无等级 > 无权限 > 永远无法满足 > 主体表已满 > 被限流。
// 任何被拒绝的调用（含被限流）不改变任何状态。
func (l *Limiter) Allow(sub, path string, scopes []string, now int64) (Result, error) {
	if sub == "" || path == "" {
		return Result{}, ErrInvalidParam
	}
	for _, s := range scopes {
		if s == "" {
			return Result{}, ErrInvalidParam
		}
	}
	if now < 0 || now > MaxNow {
		return Result{}, ErrInvalidTime
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now < l.maxNow {
		return Result{}, ErrClockRollback
	}
	r, ok := l.routes[path]
	if !ok {
		return Result{}, ErrRouteNotFound
	}
	t, err := l.tiers.Derive(scopes)
	if err != nil {
		return Result{}, ErrNoTier
	}
	if r.scope != "" && !hasScope(scopes, r.scope) {
		return Result{}, ErrForbidden
	}
	prevTAT, hasTAT := l.tats[sub]
	out, err := gcra.Check(prevTAT, hasTAT, now, r.cost, t.T, t.B)
	if err != nil {
		return Result{}, ErrNeverSatisfiable
	}
	if !out.Allowed {
		return Result{
			Allowed:    false,
			Limit:      t.B,
			Remaining:  out.Remaining,
			Reset:      out.Reset,
			RetryAfter: out.RetryAfter,
			Tier:       t.Name,
		}, ErrLimited
	}
	// 容量判定：占用数为 TAT > now 的主体数。
	occupying := hasTAT && prevTAT > now
	if !occupying {
		n := 0
		for id, tat := range l.tats {
			if tat > now {
				n++
			} else {
				delete(l.tats, id) // 惰性回收空闲条目，对外不可见
			}
		}
		if n >= l.cap {
			return Result{}, ErrTableFull
		}
	}
	l.tats[sub] = out.New
	l.maxNow = now
	return Result{
		Allowed:   true,
		Limit:     t.B,
		Remaining: out.Remaining,
		Reset:     out.Reset,
		Tier:      t.Name,
	}, nil
}
