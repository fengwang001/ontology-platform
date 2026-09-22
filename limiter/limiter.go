// Package limiter provides a multi-tenant two-level rate limiter.
// Every request must pass both the tenant bucket and a shared global
// bucket; a rejection never consumes tokens from either bucket.
// It depends on bucket and policy; nothing depends back on it.
package limiter

import (
	"errors"
	"sync"
	"time"

	"ontology/bucket"
	"ontology/policy"
)

// Distinguishable rejection reasons, usable with errors.Is.
var (
	// ErrTenantNotFound: the tenant is not registered.
	ErrTenantNotFound = errors.New("limiter: tenant not found")
	// ErrTenantExists: the tenant is already registered.
	ErrTenantExists = errors.New("limiter: tenant already registered")
	// ErrTenantQuota: the tenant bucket lacks tokens.
	ErrTenantQuota = errors.New("limiter: tenant quota exhausted")
	// ErrGlobalQuota: the global bucket lacks tokens.
	ErrGlobalQuota = errors.New("limiter: global quota exhausted")
	// ErrInvalidAmount: n <= 0.
	ErrInvalidAmount = errors.New("limiter: amount must be positive")
	// ErrExceedsBurst: n exceeds a bucket's capacity; the request
	// can never be admitted and is rejected immediately.
	ErrExceedsBurst = errors.New("limiter: amount exceeds burst capacity")
)

// Limiter manages per-tenant buckets plus one shared global bucket.
// It is safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	global  *bucket.Bucket
	tenants map[string]*bucket.Bucket
	quotas  map[string]policy.Quota
}

// New creates a Limiter with the given global quota and injected
// clock. The global bucket starts full.
func New(global policy.Quota, now func() time.Time) (*Limiter, error) {
	g, err := policy.Normalize(global)
	if err != nil {
		return nil, err
	}
	return &Limiter{
		now:     now,
		global:  bucket.New(g.Capacity, g.RatePerSec, now),
		tenants: make(map[string]*bucket.Bucket),
		quotas:  make(map[string]policy.Quota),
	}, nil
}

// Allow admits a request of n tokens for tenant only if both the
// tenant bucket and the global bucket can cover n. On any rejection
// both buckets are left exactly as they were before the call.
//
// Errors are distinguishable with errors.Is: ErrTenantNotFound,
// ErrInvalidAmount, ErrExceedsBurst, ErrTenantQuota, ErrGlobalQuota.
// When both buckets are short, ErrTenantQuota is reported (tenant
// first).
func (l *Limiter) Allow(tenant string, n float64) error {
	if n <= 0 {
		return ErrInvalidAmount
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	tb, ok := l.tenants[tenant]
	if !ok {
		return ErrTenantNotFound
	}
	if n > tb.Capacity() || n > l.global.Capacity() {
		return ErrExceedsBurst
	}
	if err := tb.TryTake(n); err != nil {
		return ErrTenantQuota
	}
	if err := l.global.TryTake(n); err != nil {
		tb.GiveBack(n)
		return ErrGlobalQuota
	}
	return nil
}
