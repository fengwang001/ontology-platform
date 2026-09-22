// Package limiter implements a multi-tenant two-level rate limiter: every
// request must draw tokens from both its tenant bucket and a shared global
// bucket. It depends on the bucket and policy packages.
package limiter

import (
	"math"
	"sync"
	"time"

	"ontology/bucket"
	"ontology/policy"
)

type tenant struct {
	bucket *bucket.Bucket
	quota  policy.Quota
}

// Limiter manages per-tenant buckets plus one shared global bucket.
// A single mutex serializes all operations so two-level Allow is atomic.
type Limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	global  *bucket.Bucket
	tenants map[string]*tenant
}

// New creates a Limiter with the given global quota and injected clock.
func New(global policy.Quota, now func() time.Time) (*Limiter, error) {
	if err := global.Check(); err != nil {
		return nil, err
	}
	return &Limiter{
		now:     now,
		global:  bucket.New(global.Capacity, global.RatePerSec, now),
		tenants: make(map[string]*tenant),
	}, nil
}

// Register adds a tenant with a fresh, full bucket.
func (l *Limiter) Register(id string, q policy.Quota) error {
	if err := q.Check(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[id]; ok {
		return ErrTenantExists
	}
	l.tenants[id] = &tenant{
		bucket: bucket.New(q.Capacity, q.RatePerSec, l.now),
		quota:  q,
	}
	return nil
}

// Unregister removes a tenant's bucket. The global bucket is untouched.
func (l *Limiter) Unregister(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[id]; !ok {
		return ErrTenantNotFound
	}
	delete(l.tenants, id)
	return nil
}

// UpdateQuota hot-updates a tenant's capacity and rate. The update itself
// neither consumes nor refills tokens: shrinking the capacity truncates
// the balance, growing it leaves the balance unchanged.
func (l *Limiter) UpdateQuota(id string, q policy.Quota) error {
	if err := q.Check(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tenants[id]
	if !ok {
		return ErrTenantNotFound
	}
	t.bucket.Update(q.Capacity, q.RatePerSec)
	t.quota = q
	return nil
}

// Allow admits a request of n tokens for the tenant only if both the
// tenant bucket and the global bucket can pay n. On any rejection both
// balances are exactly what they were before the call.
func (l *Limiter) Allow(id string, n float64) error {
	if n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return ErrInvalidAmount
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tenants[id]
	if !ok {
		return ErrTenantNotFound
	}
	if n > t.quota.Capacity || n > l.global.Capacity() {
		return ErrBurstExceeded
	}
	// Take from the tenant first; if the global bucket cannot pay,
	// roll the tenant bucket back so a rejection changes nothing.
	if !t.bucket.TryTake(n) {
		return ErrTenantQuota
	}
	if !l.global.TryTake(n) {
		t.bucket.Rollback(n)
		return ErrGlobalQuota
	}
	return nil
}

// Stats reports the tenant's current balance, the global balance and the
// tenant's quota. It only advances clock-driven refill; it never consumes
// tokens. An unregistered tenant yields zero values and ok == false.
func (l *Limiter) Stats(id string) (tenantTokens, globalTokens float64, q policy.Quota, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, exists := l.tenants[id]
	if !exists {
		return 0, 0, policy.Quota{}, false
	}
	return t.bucket.Balance(), l.global.Balance(), t.quota, true
}
