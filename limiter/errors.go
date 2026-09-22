package limiter

import "errors"

// Rejection and misuse errors, distinguishable with errors.Is.
var (
	// ErrTenantQuota is returned when the tenant bucket lacks tokens.
	// When both buckets are short, the tenant cause is reported first.
	ErrTenantQuota = errors.New("limiter: tenant quota exceeded")
	// ErrGlobalQuota is returned when the shared global bucket lacks tokens.
	ErrGlobalQuota = errors.New("limiter: global quota exceeded")
	// ErrTenantNotFound is returned for Allow/Inspect on unknown tenants.
	ErrTenantNotFound = errors.New("limiter: tenant not registered")
	// ErrTenantExists is returned when registering a duplicate tenant.
	ErrTenantExists = errors.New("limiter: tenant already registered")
	// ErrInvalidAmount is returned when n <= 0. No tokens are consumed.
	ErrInvalidAmount = errors.New("limiter: amount must be > 0")
	// ErrExceedsBurst is returned immediately when n exceeds a bucket's
	// capacity. This limiter never blocks waiting for tokens.
	ErrExceedsBurst = errors.New("limiter: amount exceeds burst capacity")
)
