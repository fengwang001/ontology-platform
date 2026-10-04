// Package approval tracks approval tickets and their validity windows.
package approval

import "errors"

// ErrDuplicateApproval is returned when a user approves while their
// previous approval is still valid.
var ErrDuplicateApproval = errors.New("approval: duplicate approval")

// Ticket records the approvals of one request as user -> approval time.
// An approval made at time `at` is valid at time t iff t < at+ttl.
type Ticket struct {
	ttl int64
	at  map[string]int64
}

// NewTicket returns an empty ticket with the given validity window.
func NewTicket(ttl int64) *Ticket {
	return &Ticket{ttl: ttl, at: make(map[string]int64)}
}

// Valid reports whether user's approval is valid at time now.
func (t *Ticket) Valid(user string, now int64) bool {
	at, ok := t.at[user]
	return ok && now < at+t.ttl
}

// Add records an approval by user at time now. A still-valid previous
// approval by the same user is rejected as a duplicate; an expired one
// is replaced with the new time.
func (t *Ticket) Add(user string, now int64) error {
	if t.Valid(user, now) {
		return ErrDuplicateApproval
	}
	t.at[user] = now
	return nil
}

// ValidCount returns the number of approvals valid at time now.
func (t *Ticket) ValidCount(now int64) int {
	count := 0
	for _, at := range t.at {
		if now < at+t.ttl {
			count++
		}
	}
	return count
}

// Entries returns a copy of the raw user -> approval time map.
func (t *Ticket) Entries() map[string]int64 {
	out := make(map[string]int64, len(t.at))
	for user, at := range t.at {
		out[user] = at
	}
	return out
}
