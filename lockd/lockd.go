// Package lockd is the outward-facing gateway of the lease-mutex service.
//
// It manages leases for many resources, all driven by a single injected
// clock, and exposes the fencing-token write check. Dependency direction is
// one-way: lockd -> lease -> fence.
package lockd

import (
	"sync"
	"time"

	"ontology/fence"
	"ontology/lease"
)

// Re-exported sentinel errors so callers only need to import lockd.
var (
	ErrHeld      = lease.ErrHeld
	ErrNotHeld   = lease.ErrNotHeld
	ErrNotHolder = lease.ErrNotHolder
	ErrStale     = fence.ErrStale
)

// Re-exported error/detail types.
type (
	HeldError      = lease.HeldError
	NotHolderError = lease.NotHolderError
	StaleError     = fence.StaleError
	Info           = lease.Info
	Token          = fence.Token
)

// Service grants, renews, releases and observes leases across many
// resources, and validates fenced writes. It is safe for concurrent use.
type Service struct {
	now func() time.Time

	mu     sync.Mutex
	leases map[string]*lease.Lease
}

// New creates a Service whose only time source is the injected clock now.
func New(now func() time.Time) *Service {
	return &Service{now: now, leases: make(map[string]*lease.Lease)}
}

// leaseFor returns the lease state machine for resource, creating it on
// first use.
func (s *Service) leaseFor(resource string) *lease.Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[resource]
	if !ok {
		l = lease.New(s.now)
		s.leases[resource] = l
	}
	return l
}

// Acquire tries to grant resource to holder for ttl. On success it returns
// a new fencing token, strictly larger than any token ever granted for this
// resource. If another client holds a valid lease it fails with
// *lease.HeldError carrying the current holder and remaining time.
func (s *Service) Acquire(resource, holder string, ttl time.Duration) (fence.Token, error) {
	return s.leaseFor(resource).Acquire(holder, ttl)
}

// Renew extends the caller's lease on resource to now+ttl. Only the current
// holder may renew, and only while the lease is still valid; the fencing
// token does not change.
func (s *Service) Renew(resource, holder string, ttl time.Duration) error {
	return s.leaseFor(resource).Renew(holder, ttl)
}

// Release makes the holder give up resource immediately. Only the first
// release by the current holder succeeds.
func (s *Service) Release(resource, holder string) error {
	return s.leaseFor(resource).Release(holder)
}

// Write accepts data for resource iff the resource is currently held and
// token is not smaller than the resource's fencing high-water mark. The two
// failure modes are distinguishable: lease.ErrNotHeld when nobody holds the
// resource, fence.StaleError when the token is too old.
func (s *Service) Write(resource string, token fence.Token, data []byte) error {
	return s.leaseFor(resource).CheckWrite(token)
}

// Info returns a read-only snapshot of resource's lease state. When nobody
// holds it, Holder is empty and Remaining is zero.
func (s *Service) Info(resource string) lease.Info {
	return s.leaseFor(resource).Info()
}
