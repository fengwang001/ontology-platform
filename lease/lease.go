// Package lease implements the lease state machine for a single resource.
//
// A lease grants one holder exclusive ownership until an expiry time.
// The holder must renew before expiry; expiry is judged lazily against an
// injected clock (no background timers). Every successful grant allocates a
// new, strictly larger fencing token from the resource's fence.Fencer.
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/fence"
)

// ErrNotHeld is returned when an operation requires a valid (unexpired)
// lease but the resource is currently not held by anyone.
var ErrNotHeld = errors.New("lease: resource not held")

// ErrNotHolder is returned (wrapped by *NotHolderError) when a holder-only
// operation is attempted by a different client.
var ErrNotHolder = errors.New("lease: caller is not the holder")

// ErrHeld is returned (wrapped by *HeldError) when Acquire fails because
// another client holds a valid lease.
var ErrHeld = errors.New("lease: resource already held")

// HeldError describes a failed Acquire: who holds the lease and how long
// remains until it expires.
type HeldError struct {
	Holder    string
	Remaining time.Duration
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("lease: held by %q for %s more", e.Holder, e.Remaining)
}

// Is reports that a HeldError matches ErrHeld.
func (e *HeldError) Is(target error) bool { return target == ErrHeld }

// NotHolderError describes a holder-only operation attempted by a client
// that does not hold the lease.
type NotHolderError struct {
	Caller string
	Holder string
}

func (e *NotHolderError) Error() string {
	return fmt.Sprintf("lease: %q is not the holder (held by %q)", e.Caller, e.Holder)
}

// Is reports that a NotHolderError matches ErrNotHolder.
func (e *NotHolderError) Is(target error) bool { return target == ErrNotHolder }

// Info is a read-only snapshot of a lease. When Held is false, Holder is
// empty and Remaining is zero; Token still reports the resource's current
// fencing token (the most recently allocated one, 0 if never granted).
type Info struct {
	Held      bool
	Holder    string
	Remaining time.Duration
	Token     fence.Token
}

// Lease is the state machine for one resource. The zero value is not
// usable; construct with New. It is safe for concurrent use.
type Lease struct {
	now    func() time.Time
	fencer *fence.Fencer

	mu     sync.Mutex
	held   bool
	holder string
	expiry time.Time
	token  fence.Token
}

// New creates a Lease whose time source is the injected clock now.
func New(now func() time.Time) *Lease {
	return &Lease{now: now, fencer: fence.New()}
}

// validLocked reports whether the lease is currently held and unexpired.
// Expiry is left-closed right-open: now == expiry already means expired.
func (l *Lease) validLocked() bool {
	return l.held && l.now().Before(l.expiry)
}

// Acquire grants the lease to holder for ttl and returns the new fencing
// token. It fails with *HeldError if another client holds a valid lease.
// Every successful grant allocates a strictly larger token.
func (l *Lease) Acquire(holder string, ttl time.Duration) (fence.Token, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.validLocked() {
		return 0, &HeldError{Holder: l.holder, Remaining: l.expiry.Sub(l.now())}
	}
	token := l.fencer.Allocate()
	l.held, l.holder, l.token = true, holder, token
	l.expiry = l.now().Add(ttl)
	return token, nil
}

// Renew extends the lease to now+ttl (not accumulated onto the old expiry).
// Only the current holder of a valid lease may renew; the fencing token is
// left unchanged. Renewing an expired lease fails with ErrNotHeld.
func (l *Lease) Renew(holder string, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() {
		return ErrNotHeld
	}
	if l.holder != holder {
		return &NotHolderError{Caller: holder, Holder: l.holder}
	}
	l.expiry = l.now().Add(ttl)
	return nil
}

// Release lets the current holder give up the lease immediately. A release
// by anyone else fails with *NotHolderError; releasing when no valid lease
// exists (including a second release) fails with ErrNotHeld.
func (l *Lease) Release(holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() {
		return ErrNotHeld
	}
	if l.holder != holder {
		return &NotHolderError{Caller: holder, Holder: l.holder}
	}
	l.held, l.holder, l.expiry, l.token = false, "", time.Time{}, 0
	return nil
}

// CheckWrite validates a write carrying fencing token t. It fails with
// ErrNotHeld when no valid lease exists, and with fence.StaleError when the
// token is older than the resource's high-water mark.
func (l *Lease) CheckWrite(t fence.Token) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() {
		return ErrNotHeld
	}
	return l.fencer.Validate(t)
}

// Info returns a consistent snapshot of the lease state.
func (l *Lease) Info() Info {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.validLocked() {
		return Info{Held: false, Holder: "", Remaining: 0, Token: l.fencer.Last()}
	}
	return Info{
		Held:      true,
		Holder:    l.holder,
		Remaining: l.expiry.Sub(l.now()),
		Token:     l.token,
	}
}
