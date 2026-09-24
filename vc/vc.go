// Package vc provides pure vector-clock predicates used by the causal
// delivery buffer. It depends on no other package.
package vc

import "errors"

// Msg is one causally-broadcast message from sender From carrying the
// sender's vector clock V of length n.
type Msg struct {
	From int
	V    []int64
}

// Sentinel errors for the two kinds of illegal input this package can see.
var (
	// ErrBadSender is returned when Msg.From is outside 0..n-1.
	ErrBadSender = errors.New("vc: sender id out of range")
	// ErrBadVector is returned when V has the wrong length, a negative
	// component, or a non-positive own sequence number.
	ErrBadVector = errors.New("vc: illegal vector clock")
)

// LessOrEqual reports whether a <= b componentwise (lengths are assumed equal).
func LessOrEqual(a, b []int64) bool {
	for i := range a {
		if a[i] > b[i] {
			return false
		}
	}
	return true
}

// Legal validates a message against the fixed sender count n: From must be
// in range, V must have length n with no negative component, and the
// sender's own sequence number must be at least 1.
func Legal(n int, m Msg) error {
	if m.From < 0 || m.From >= n {
		return ErrBadSender
	}
	if len(m.V) != n {
		return ErrBadVector
	}
	for _, x := range m.V {
		if x < 0 {
			return ErrBadVector
		}
	}
	if m.V[m.From] < 1 {
		return ErrBadVector
	}
	return nil
}

// AlreadyDelivered reports whether m was delivered before: its own sequence
// number is at most the count already delivered from that sender.
func AlreadyDelivered(m Msg, local []int64) bool {
	return m.V[m.From] <= local[m.From]
}

// Deliverable reports the causal delivery condition for a message from
// sender j: its own slot must be exactly the next expected number, and
// every other component must not exceed what the receiver has delivered.
func Deliverable(m Msg, local []int64) bool {
	j := m.From
	if m.V[j] != local[j]+1 {
		return false
	}
	for k := range local {
		if k != j && m.V[k] > local[k] {
			return false
		}
	}
	return true
}
