package limiter

import (
	"ontology/bucket"
	"ontology/policy"
)

// Snapshot is a read-only view of a tenant's state and the shared
// global state at one instant.
type Snapshot struct {
	// Quota is the tenant's current normalized quota.
	Quota policy.Quota
	// TenantBalance is the tenant bucket's current token count.
	TenantBalance float64
	// GlobalBalance is the global bucket's current token count.
	GlobalBalance float64
}

// Register adds a tenant with a fresh, full bucket. It returns
// ErrTenantExists if the tenant is already registered, or a policy
// error if the quota is invalid.
func (l *Limiter) Register(tenant string, quota policy.Quota) error {
	q, err := policy.Normalize(quota)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[tenant]; ok {
		return ErrTenantExists
	}
	l.tenants[tenant] = bucket.New(q.Capacity, q.RatePerSec, l.now)
	l.quotas[tenant] = q
	return nil
}

// Unregister removes a tenant's bucket. Re-registering later starts
// a brand-new full bucket. The global bucket is untouched. It
// returns ErrTenantNotFound if the tenant does not exist.
func (l *Limiter) Unregister(tenant string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.tenants[tenant]; !ok {
		return ErrTenantNotFound
	}
	delete(l.tenants, tenant)
	delete(l.quotas, tenant)
	return nil
}

// SetQuota hot-updates a tenant's capacity and rate, effective
// immediately. Shrinking capacity truncates the current balance;
// growing capacity leaves the balance unchanged. The update itself
// neither consumes nor grants tokens.
func (l *Limiter) SetQuota(tenant string, quota policy.Quota) error {
	q, err := policy.Normalize(quota)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tb, ok := l.tenants[tenant]
	if !ok {
		return ErrTenantNotFound
	}
	tb.Reconfigure(q.Capacity, q.RatePerSec)
	l.quotas[tenant] = q
	return nil
}

// Inspect reports the tenant's current quota and balance plus the
// global balance. It consumes no tokens and changes nothing beyond
// time-based refill, so two calls at the same instant agree. The
// second return value is false (and the Snapshot zero) when the
// tenant is not registered.
func (l *Limiter) Inspect(tenant string) (Snapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	tb, ok := l.tenants[tenant]
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{
		Quota:         l.quotas[tenant],
		TenantBalance: tb.Balance(),
		GlobalBalance: l.global.Balance(),
	}, true
}

// GlobalBalance reports the global bucket's current token count.
func (l *Limiter) GlobalBalance() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.global.Balance()
}
