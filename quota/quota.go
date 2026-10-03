// Package quota implements a per-tenant token bucket with lazy refill.
// Tokens are accounted in milli-bytes: a bucket of Burst bytes holds
// Burst*1000 tokens and refills Rate*delta tokens over delta milliseconds.
package quota

import "errors"

const (
	MaxTenants = 1000
	MaxNameLen = 64
	MaxRate    = 1_000_000_000
	MaxBurst   = 1_000_000_000
	MaxPrio    = 2
)

var (
	ErrInvalidParam   = errors.New("quota: invalid parameter")
	ErrDuplicateName  = errors.New("quota: duplicate tenant name")
	ErrTooManyTenants = errors.New("quota: tenant limit reached")
)

// Tenant is a single token bucket. Not safe for concurrent use; the
// caller (gateway) serializes access.
type Tenant struct {
	Name       string
	Rate       int64 // bytes per second
	Burst      int64 // bucket capacity in bytes
	MaxPrio    int   // highest (smallest number) priority allowed
	tokens     int64 // milli-bytes, always within [0, Burst*1000]
	lastRefill int64 // last settle time in milliseconds
}

func (t *Tenant) cap() int64 { return t.Burst * 1000 }

// refillAmount returns the tokens gained over delta ms, pre-capped so the
// multiplication can never overflow int64 (rate*delta may reach 1e21).
func (t *Tenant) refillAmount(delta int64) int64 {
	if delta <= 0 || t.Rate == 0 {
		return 0
	}
	c := t.cap()
	if delta > c/t.Rate {
		return c
	}
	return t.Rate * delta
}

// refillTo settles the bucket at now, persisting lastRefill.
func (t *Tenant) refillTo(now int64) {
	if now <= t.lastRefill {
		return
	}
	t.tokens += t.refillAmount(now - t.lastRefill)
	if c := t.cap(); t.tokens > c {
		t.tokens = c
	}
	t.lastRefill = now
}

// Balance reports the settled balance at now without mutating state.
func (t *Tenant) Balance(now int64) int64 {
	b := t.tokens
	if now > t.lastRefill {
		b += t.refillAmount(now - t.lastRefill)
	}
	if c := t.cap(); b > c {
		b = c
	}
	return b
}

// CanSpend reports whether the virtual balance at now covers milliTokens.
func (t *Tenant) CanSpend(now int64, milliTokens int64) bool {
	return t.Balance(now) >= milliTokens
}

// Spend settles at now and subtracts milliTokens. Callers must have
// checked CanSpend first.
func (t *Tenant) Spend(now int64, milliTokens int64) {
	t.refillTo(now)
	t.tokens -= milliTokens
}

// Refund settles at now and credits milliTokens back, capped at capacity.
func (t *Tenant) Refund(now int64, milliTokens int64) {
	t.refillTo(now)
	t.tokens += milliTokens
	if c := t.cap(); t.tokens > c {
		t.tokens = c
	}
}

// Tokens returns the persisted (not virtually settled) balance.
func (t *Tenant) Tokens() int64 { return t.tokens }

// LastRefill returns the last persisted settle time.
func (t *Tenant) LastRefill() int64 { return t.lastRefill }

// Registry holds tenants by name, preserving insertion order.
type Registry struct {
	byName map[string]*Tenant
	order  []*Tenant
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]*Tenant)}
}

// Add registers a tenant. Errors, in order: invalid parameter, duplicate
// name, tenant limit reached. It never touches any clock state.
func (r *Registry) Add(name string, rate, burst int64, maxPrio int) (*Tenant, error) {
	if name == "" || len(name) > MaxNameLen ||
		rate < 0 || rate > MaxRate ||
		burst < 1 || burst > MaxBurst ||
		maxPrio < 0 || maxPrio > MaxPrio {
		return nil, ErrInvalidParam
	}
	if _, ok := r.byName[name]; ok {
		return nil, ErrDuplicateName
	}
	if len(r.order) >= MaxTenants {
		return nil, ErrTooManyTenants
	}
	t := &Tenant{
		Name:    name,
		Rate:    rate,
		Burst:   burst,
		MaxPrio: maxPrio,
		tokens:  burst * 1000,
	}
	r.byName[name] = t
	r.order = append(r.order, t)
	return t, nil
}

// Get returns the tenant or nil.
func (r *Registry) Get(name string) *Tenant { return r.byName[name] }

// All returns tenants in insertion order.
func (r *Registry) All() []*Tenant { return r.order }
