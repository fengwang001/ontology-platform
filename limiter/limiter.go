// Package limiter provides a multi-tenant, two-level rate limiter: every
// request must pass both its tenant bucket and a shared global bucket.
package limiter

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/bucket"
	"ontology/policy"
)

// Limiter manages one token bucket per tenant plus a shared global bucket.
// All state lives in process memory. It is safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	global  *bucket.Bucket
	tenants map[string]*bucket.Bucket
	quotas  map[string]policy.Quota
}

// New returns a Limiter with the given global quota. now is the only time
// source; it is never expected to move backwards (a rewind is ignored).
func New(global policy.Quota, now func() time.Time) *Limiter {
	return &Limiter{
		now:     now,
		global:  bucket.New(global.Capacity, global.RatePerSec, now),
		tenants: make(map[string]*bucket.Bucket),
		quotas:  make(map[string]policy.Quota),
	}
}

// Register adds a tenant with a fresh, full bucket.
func (l *Limiter) Register(tenant string, q policy.Quota) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[tenant]; ok {
		return fmt.Errorf("%w: %q", ErrTenantExists, tenant)
	}
	l.tenants[tenant] = bucket.New(q.Capacity, q.RatePerSec, l.now)
	l.quotas[tenant] = q
	return nil
}

// Unregister removes a tenant's bucket. The global bucket is untouched.
// Re-registering later starts over with a full bucket.
func (l *Limiter) Unregister(tenant string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[tenant]; !ok {
		return fmt.Errorf("%w: %q", ErrTenantNotFound, tenant)
	}
	delete(l.tenants, tenant)
	delete(l.quotas, tenant)
	return nil
}

// SetQuota hot-updates a tenant's capacity and rate. The change takes
// effect immediately and neither consumes nor refills tokens: shrinking
// the capacity truncates the current balance, growing it does not top up.
func (l *Limiter) SetQuota(tenant string, q policy.Quota) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.tenants[tenant]
	if !ok {
		return fmt.Errorf("%w: %q", ErrTenantNotFound, tenant)
	}
	b.SetLimit(q.Capacity, q.RatePerSec)
	l.quotas[tenant] = q
	return nil
}

// Allow deducts n tokens from both the tenant and global buckets, or from
// neither. On rejection both balances are exactly as before the call.
func (l *Limiter) Allow(tenant string, n int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidAmount, n)
	}
	b, ok := l.tenants[tenant]
	if !ok {
		return fmt.Errorf("%w: %q", ErrTenantNotFound, tenant)
	}
	amt := float64(n)
	if amt > b.Capacity() || amt > l.global.Capacity() {
		return fmt.Errorf("%w: %d", ErrExceedsBurst, n)
	}
	if !b.TryTake(amt) {
		return fmt.Errorf("%w: %q needs %d", ErrTenantQuota, tenant, n)
	}
	if !l.global.TryTake(amt) {
		b.Refund(amt)
		return fmt.Errorf("%w: %q needs %d", ErrGlobalQuota, tenant, n)
	}
	return nil
}

// Inspect reports the tenant's balance, the global balance, and the
// tenant's quota. ok is false (and all values zero) for unknown tenants.
// It advances time-based refill only; it never consumes tokens, so two
// calls at the same instant return identical results.
func (l *Limiter) Inspect(tenant string) (tenantBal, globalBal float64, q policy.Quota, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, found := l.tenants[tenant]
	if !found {
		return 0, 0, policy.Quota{}, false
	}
	return b.Balance(), l.global.Balance(), l.quotas[tenant], true
}

// IsTenantQuota reports whether err is a tenant-quota rejection.
func IsTenantQuota(err error) bool { return errors.Is(err, ErrTenantQuota) }

// IsGlobalQuota reports whether err is a global-quota rejection.
func IsGlobalQuota(err error) bool { return errors.Is(err, ErrGlobalQuota) }
