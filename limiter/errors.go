package limiter

import "errors"

var (
	// ErrTenantNotFound reports an Allow/UpdateQuota/Unregister/Stats
	// call for a tenant that is not registered.
	ErrTenantNotFound = errors.New("limiter: tenant not registered")
	// ErrTenantExists reports a Register call for an existing tenant.
	ErrTenantExists = errors.New("limiter: tenant already registered")
	// ErrInvalidAmount reports a non-positive or non-finite n.
	ErrInvalidAmount = errors.New("limiter: amount must be a positive finite number")
	// ErrBurstExceeded reports n larger than a bucket capacity; such a
	// request can never be admitted and is rejected immediately.
	ErrBurstExceeded = errors.New("limiter: amount exceeds burst capacity")
	// ErrTenantQuota reports rejection because the tenant bucket lacks
	// tokens. It takes priority over ErrGlobalQuota when both are short.
	ErrTenantQuota = errors.New("limiter: tenant quota exhausted")
	// ErrGlobalQuota reports rejection because the global bucket lacks
	// tokens while the tenant bucket had enough.
	ErrGlobalQuota = errors.New("limiter: global quota exhausted")
)
