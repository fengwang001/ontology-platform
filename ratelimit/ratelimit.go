// Package ratelimit 实现网关限流器：权限先于限流，GCRA 判定，
// 并生成可精确复现的限流响应头。
//
// 所有方法可并发调用，结果等价于某个串行顺序；
// 相同的操作序列重放得到完全相同的结果与响应头。
package ratelimit

import (
	"errors"
	"sync"

	"ontology/gcra"
	"ontology/tier"
)

// 拒绝按此顺序只报第一个。
var (
	ErrInvalidArgument  = errors.New("ratelimit: 参数非法")
	ErrInvalidTime      = errors.New("ratelimit: 时间非法")
	ErrClockRegression  = errors.New("ratelimit: 时钟回退")
	ErrRouteNotFound    = errors.New("ratelimit: 路由不存在")
	ErrNoTier           = errors.New("ratelimit: 无等级")
	ErrForbidden        = errors.New("ratelimit: 无权限")
	ErrImpossible       = errors.New("ratelimit: 永远无法满足")
	ErrSubjectTableFull = errors.New("ratelimit: 主体表已满")
	ErrLimited          = errors.New("ratelimit: 被限流")
	// ErrDuplicate 路由 path 或等级 name/rank 已存在。
	ErrDuplicate = errors.New("ratelimit: 已存在")
)

const (
	maxNow      = 1_000_000_000_000_000 // 10^15 毫秒
	maxCapacity = 1_000_000
	maxCost     = 1_000_000
)

// Result 是 Allow 的结果。
// error 非 nil 且不是 ErrLimited 时，返回零值 Result（不带响应头）；
// 被限流时错误为 ErrLimited，Result 携带完整响应头（含 RetryAfter）。
type Result struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	Reset      int64
	RetryAfter int64 // 仅被限流时有效
	Tier       string
}

type route struct {
	scope string
	cost  int64
}

// Limiter 是网关限流器。单互斥锁串行化全部操作。
type Limiter struct {
	mu       sync.Mutex
	capacity int64
	tiers    *tier.Registry
	routes   map[string]route
	tat      map[string]int64 // sub -> 理论到达时刻（仅存有欠账的主体）
	maxNow   int64            // 已放行请求的最大 now，初值 0
}

// New 构造限流器，S 为主体表容量（1 到 10^6）。
func New(capacity int64) (*Limiter, error) {
	if capacity < 1 || capacity > maxCapacity {
		return nil, ErrInvalidArgument
	}
	return &Limiter{
		capacity: capacity,
		tiers:    tier.NewRegistry(),
		routes:   map[string]route{},
		tat:      map[string]int64{},
	}, nil
}

// AddTier 登记等级。拒绝顺序：参数非法 > 重复（name 或 rank 已存在）。
// 已登记的等级不可修改或删除。
func (l *Limiter) AddTier(name string, rank int, t, b int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.tiers.Add(name, rank, t, b)
	if errors.Is(err, tier.ErrInvalidArgument) {
		return ErrInvalidArgument
	}
	if errors.Is(err, tier.ErrDuplicate) {
		return ErrDuplicate
	}
	return err
}

// AddRoute 登记路由。path 非空且唯一（精确匹配），scope 可为空串表示公开，
// cost 在 1 到 10^6。拒绝顺序：参数非法 > 重复。已登记的路由不可修改或删除。
func (l *Limiter) AddRoute(path, scope string, cost int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if path == "" || cost < 1 || cost > maxCost {
		return ErrInvalidArgument
	}
	if _, ok := l.routes[path]; ok {
		return ErrDuplicate
	}
	l.routes[path] = route{scope: scope, cost: cost}
	return nil
}

// Allow 判定一次请求。时间单位毫秒。
// 任何被拒绝的操作（含被限流）都不改变 TAT、最大 now 与主体表。
func (l *Limiter) Allow(sub, path string, scopes []string, now int64) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if sub == "" || path == "" {
		return Result{}, ErrInvalidArgument
	}
	for _, s := range scopes {
		if s == "" {
			return Result{}, ErrInvalidArgument
		}
	}
	if now < 0 || now > maxNow {
		return Result{}, ErrInvalidTime
	}
	if now < l.maxNow {
		return Result{}, ErrClockRegression
	}
	rt, ok := l.routes[path]
	if !ok {
		return Result{}, ErrRouteNotFound
	}
	tr, err := l.tiers.Derive(scopes)
	if err != nil {
		return Result{}, ErrNoTier
	}
	if rt.scope != "" && !hasScope(scopes, rt.scope) {
		return Result{}, ErrForbidden
	}
	if !gcra.Possible(rt.cost, tr.B) {
		return Result{}, ErrImpossible
	}

	// 回收空闲条目（TAT<=now），对外不可见；此后占用数即 len(l.tat)。
	for s, v := range l.tat {
		if v <= now {
			delete(l.tat, s)
		}
	}
	tat, has := l.tat[sub]
	if !has && int64(len(l.tat)) >= l.capacity {
		return Result{}, ErrSubjectTableFull
	}

	d := gcra.Evaluate(tat, has, now, rt.cost, tr.T, tr.B)
	res := Result{
		Allowed:    d.Allowed,
		Limit:      d.Limit,
		Remaining:  d.Remaining,
		Reset:      d.Reset,
		RetryAfter: d.RetryAfter,
		Tier:       tr.Name,
	}
	if !d.Allowed {
		return res, ErrLimited
	}
	l.tat[sub] = d.NewTAT
	l.maxNow = now
	return res, nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
