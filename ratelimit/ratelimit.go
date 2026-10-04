// Package ratelimit is the gateway limiter: scope-based tiers, GCRA, headers.
package ratelimit

import (
	"sync"

	"ontology/gcra"
	"ontology/tier"
)

// Result reports the outcome of Allow.
type Result struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	Reset      int64
	RetryAfter int64
	Tier       string
}

type route struct {
	scope string
	cost  int64
}

// Limiter is the concurrency-safe gateway rate limiter.
type Limiter struct {
	mu       sync.Mutex
	registry *tier.Registry
	routes   map[string]route
	tat      map[string]int64
	maxNow   int64
	capacity int64
}

// New creates a Limiter with subject-table capacity S.
func New(capacity int64) (*Limiter, error) {
	if capacity < 1 || capacity > 1_000_000 {
		return nil, ErrInvalidArg
	}
	return &Limiter{
		registry: tier.NewRegistry(),
		routes:   make(map[string]route),
		tat:      make(map[string]int64),
		capacity: capacity,
	}, nil
}

// AddTier registers a tier.
func (l *Limiter) AddTier(name string, rank, t, b int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch err := l.registry.Add(name, rank, t, b); err {
	case nil:
		return nil
	case tier.ErrInvalidArg:
		return ErrInvalidArg
	default:
		return ErrDuplicate
	}
}

// AddRoute registers an exact-match route.
func (l *Limiter) AddRoute(path, scope string, cost int64) error {
	if path == "" || cost < 1 || cost > 1_000_000 {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.routes[path]; ok {
		return ErrDuplicate
	}
	l.routes[path] = route{scope: scope, cost: cost}
	return nil
}

// Allow checks permission, capacity and the GCRA bucket.
func (l *Limiter) Allow(sub, path string, scopes []string, now int64) (Result, error) {
	if sub == "" || path == "" {
		return Result{}, ErrInvalidArg
	}
	for _, scope := range scopes {
		if scope == "" {
			return Result{}, ErrInvalidArg
		}
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return Result{}, ErrInvalidTime
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now < l.maxNow {
		return Result{}, ErrClockRewind
	}
	rt, ok := l.routes[path]
	if !ok {
		return Result{}, ErrNoRoute
	}
	tr, err := l.registry.Resolve(scopes)
	if err != nil {
		return Result{}, ErrNoTier
	}
	if rt.scope != "" {
		granted := false
		for _, scope := range scopes {
			if scope == rt.scope {
				granted = true
				break
			}
		}
		if !granted {
			return Result{}, ErrForbidden
		}
	}

	if rt.cost > tr.B {
		// Never satisfiable: no headers, no state change.
		return Result{}, ErrNeverAllowed
	}

	prior, seen := l.tat[sub]
	if !seen {
		prior = -1
	}
	d := gcra.Judge(tr.T, tr.B, rt.cost, now, prior)

	active := int64(0)
	for s, v := range l.tat {
		if v <= now {
			// Reclaim idle entries; invisible externally.
			delete(l.tat, s)
			if s == sub {
				seen = false
			}
			continue
		}
		active++
	}
	if (!seen || prior <= now) && active >= l.capacity {
		return Result{}, ErrTableFull
	}

	if !d.Allowed {
		// Rate limited: TAT unchanged, headers are returned together with the error.
		return Result{
			Allowed:    false,
			Limit:      d.Headers.Limit,
			Remaining:  d.Headers.Remaining,
			Reset:      d.Headers.Reset,
			RetryAfter: d.Headers.RetryAfter,
			Tier:       tr.Name,
		}, ErrRateLimited
	}

	l.tat[sub] = d.TAT
	l.maxNow = now
	return Result{
		Allowed:   true,
		Limit:     d.Headers.Limit,
		Remaining: d.Headers.Remaining,
		Reset:     d.Headers.Reset,
		Tier:      tr.Name,
	}, nil
}
