// Package lease holds a single lease: its fencing token, owner and expiry.
// It depends on no other package.
package lease

import "errors"

// Sentinel errors returned by Renew. Both are distinguishable via errors.Is.
var (
	// ErrStaleToken means the presented token is not the current one: the
	// caller has been fenced off by a newer Grant.
	ErrStaleToken = errors.New("lease: stale fencing token")
	// ErrExpired means now >= expiry: a dead lease cannot be renewed and
	// must be granted again.
	ErrExpired = errors.New("lease: lease has expired")
)

// Lease is one named lease. The zero value is not usable; use New.
type Lease struct {
	ttl    int
	token  int
	owner  string
	expiry int
}

// New returns a lease with the fixed time-to-live ttl. Before the first
// Grant it is considered expired (token 0).
func New(ttl int) *Lease { return &Lease{ttl: ttl} }

// Grant awards the lease: token strictly increases by one, owner is
// recorded and expiry becomes now+TTL. It returns the new token.
func (l *Lease) Grant(owner string, now int) int {
	l.token++
	l.owner = owner
	l.expiry = now + l.ttl
	return l.token
}

// Renew extends the lease while it is alive. The token is checked first so
// a fenced holder is rejected even before liveness; then now >= expiry is
// rejected (expiry is left-closed). Only after both checks does expiry
// move, so every rejected call leaves the state untouched.
func (l *Lease) Renew(token, now int) error {
	if token != l.token {
		return ErrStaleToken
	}
	if now >= l.expiry {
		return ErrExpired
	}
	l.expiry = now + l.ttl
	return nil
}

// Expired reports whether the lease is dead at now. Expiry is left-closed:
// now == expiry is already expired. A lease never granted (expiry 0) is
// expired for every nonnegative now.
func (l *Lease) Expired(now int) bool { return now >= l.expiry }

// Token returns the current fencing token (0 before the first Grant).
func (l *Lease) Token() int { return l.token }

// Owner returns the current holder.
func (l *Lease) Owner() string { return l.owner }

// Expiry returns the current expiry instant.
func (l *Lease) Expiry() int { return l.expiry }
