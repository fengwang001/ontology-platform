package gate

import (
	"errors"
	"sync"

	"ontology/introspect"
	"ontology/scope"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrInvalidTime     = errors.New("invalid time")
	ErrClockRollback   = errors.New("clock rollback")
)

type Gateway struct {
	mu     sync.Mutex
	cache  *introspect.Cache
	grace  int64
	nb     map[string]int64
	maxNow int64
}

func New(positive, negative, grace int64, upstream introspect.Func) (*Gateway, error) {
	if !validWindow(positive) || !validWindow(negative) || !validWindow(grace) || upstream == nil {
		return nil, ErrInvalidArgument
	}
	return &Gateway{
		cache: introspect.New(positive, negative, upstream),
		grace: grace,
		nb:    map[string]int64{},
	}, nil
}

func (g *Gateway) Check(token string, needed []string, now int64) (Decision, error) {
	if token == "" || len(needed) == 0 || containsEmpty(needed) {
		return Decision{}, ErrInvalidArgument
	}
	if !validTime(now) {
		return Decision{}, ErrInvalidTime
	}
	g.mu.Lock()
	if now < g.maxNow {
		g.mu.Unlock()
		return Decision{}, ErrClockRollback
	}
	g.maxNow = now
	g.mu.Unlock()

	entry, upstreamSource, stale, err := g.cache.Resolve(token, now)
	decisionSource := convertSource(upstreamSource)
	if err != nil {
		high := scope.High(needed)
		if !high && canReuse(stale, now, g.grace) {
			entry = stale
			decisionSource = Stale
		} else {
			return Decision{Verdict: Unavailable, Reason: "upstream unavailable"}, nil
		}
	}
	return g.decide(now, entry, needed, decisionSource), nil
}

func (g *Gateway) Revoke(sub string, at, now int64) error {
	if sub == "" || !validTime(at) {
		return ErrInvalidArgument
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return ErrClockRollback
	}
	g.maxNow = now
	revokedAt := int64(-1)
	if value, ok := g.nb[sub]; ok {
		revokedAt = value
	}
	if at > revokedAt {
		g.nb[sub] = at
	}
	return nil
}

func (g *Gateway) Calls() int64 {
	return g.cache.Calls()
}

func (g *Gateway) Len() int {
	return g.cache.Len()
}

func (g *Gateway) RevokeTouchedEntries() int64 {
	return g.cache.RevokeTouchedEntries()
}

func (g *Gateway) decide(now int64, entry introspect.Entry, needed []string, source Source) Decision {
	result := entry.Result
	if !result.Active {
		return Decision{Verdict: Inactive, Reason: "token inactive", Source: source}
	}
	g.mu.Lock()
	revokedAt, revoked := g.nb[result.Sub]
	if !revoked {
		revokedAt = -1
	}
	g.mu.Unlock()
	if result.Iat <= revokedAt {
		return Decision{Verdict: Revoked, Reason: "iat before revocation", Source: source}
	}
	if now >= result.Exp {
		return Decision{Verdict: Expired, Reason: "now at or after exp", Source: source}
	}
	if missing, ok := scope.FirstMissing(result.Scopes, needed); ok {
		return Decision{Verdict: Scope, Reason: "missing scope", Source: source, Missing: missing}
	}
	return Decision{Verdict: Allow, Reason: "authorized", Source: source}
}

func convertSource(source introspect.Source) Source {
	if source == introspect.SourceCache {
		return Cache
	}
	return Fresh
}

func canReuse(entry introspect.Entry, now, grace int64) bool {
	result := entry.Result
	return result.Active && entry.Until <= now && now < entry.Until+grace && now < result.Exp
}

func containsEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func validWindow(value int64) bool {
	return value >= 1 && value <= 1_000_000_000
}

func validTime(value int64) bool {
	return value >= 0 && value <= 100_000_000_000_000
}
