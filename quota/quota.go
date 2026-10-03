// Package quota keeps the per-tenant usage ledger and two-phase
// reservation tokens. A writer first reserves the net delta d, then
// either commits (install succeeded) or releases (install failed).
package quota

import "sync"

// Ledger tracks quota Q, usage U and pending positive reservations
// per tenant. All methods are safe for concurrent use.
type Ledger struct {
	mu       sync.Mutex
	quota    map[string]int64
	usage    map[string]int64
	reserved map[string]int64 // sum of pending positive deltas
}

// New returns an empty ledger. Tenants default to Q=0, U=0.
func New() *Ledger {
	return &Ledger{
		quota:    make(map[string]int64),
		usage:    make(map[string]int64),
		reserved: make(map[string]int64),
	}
}

// SetQuota sets the tenant's quota. It is always accepted, even when
// the new quota is below the current usage (over-quota state).
func (l *Ledger) SetQuota(t string, q int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.quota[t] = q
}

// Quota returns the tenant's quota (0 when never set).
func (l *Ledger) Quota(t string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quota[t]
}

// Usage returns the tenant's committed usage U.
func (l *Ledger) Usage(t string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.usage[t]
}

// Reserved returns the tenant's sum of pending positive reservations.
func (l *Ledger) Reserved(t string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reserved[t]
}

// Token is a one-shot reservation handle returned by Reserve.
type Token struct {
	l    *Ledger
	t    string
	d    int64
	done bool
}

// Reserve reserves the net delta d for tenant t. It succeeds when
// d <= 0, or when U+reserved+d <= Q (equality passes); concurrent
// writers therefore observe U plus all pending reservations and can
// never exceed Q together. Only positive d is held as a reservation.
func (l *Ledger) Reserve(t string, d int64) (*Token, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d > 0 {
		if l.usage[t]+l.reserved[t]+d > l.quota[t] {
			return nil, false
		}
		l.reserved[t] += d
	}
	return &Token{l: l, t: t, d: d}, true
}

// Commit turns the reservation into a usage increment. It must be
// called exactly once per token, after the install succeeded.
func (tok *Token) Commit() {
	l := tok.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if tok.done {
		panic("quota: token used twice")
	}
	tok.done = true
	if tok.d > 0 {
		l.reserved[tok.t] -= tok.d
	}
	l.usage[tok.t] += tok.d
}

// Release rolls the reservation back without touching usage. It must
// be called exactly once per token, after the install failed.
func (tok *Token) Release() {
	l := tok.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if tok.done {
		panic("quota: token used twice")
	}
	tok.done = true
	if tok.d > 0 {
		l.reserved[tok.t] -= tok.d
	}
}
