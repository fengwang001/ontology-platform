// Package rb manages per-sender trackers, dispatches deliveries and
// enforces the reorder-buffer limit. All rejection checks happen
// before any state is touched, so a rejected delivery leaves no trace.
package rb

import (
	"errors"
	"sync"

	"ontology/seq"
)

// Decidable, mutually distinct rejection reasons.
var (
	ErrEmptySender = errors.New("rb: empty sender")
	ErrNegativeSeq = errors.New("rb: negative seq")
	ErrBufferFull  = errors.New("rb: reorder buffer limit exceeded")
)

// Receiver is a multi-sender reliable-broadcast receiver.
type Receiver struct {
	mu  sync.Mutex
	max int // maxBuffered: reorder-buffer capacity per sender
	m   map[string]*seq.Tracker
}

// New returns a Receiver with the given per-sender buffer limit.
func New(maxBuffered int) *Receiver {
	return &Receiver{max: maxBuffered, m: make(map[string]*seq.Tracker)}
}

// Deliver applies one arrival. Any rejection is total: no tracker is
// created and no buffer, log or counter changes.
func (r *Receiver) Deliver(sender string, s int64, data any) error {
	if sender == "" {
		return ErrEmptySender
	}
	if s < 0 {
		return ErrNegativeSeq
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.m[sender]
	if t != nil && s > t.Next() && !t.Has(s) && t.Buffered() >= r.max {
		return ErrBufferFull
	}
	if t == nil {
		t = seq.New()
		r.m[sender] = t
	}
	t.Add(s, data)
	return nil
}

// Delivered returns the sender's delivered log (ascending seq from 0).
func (r *Receiver) Delivered(sender string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.m[sender]; t != nil {
		return t.Log()
	}
	return nil
}

// Dup returns the sender's duplicate arrival count.
func (r *Receiver) Dup(sender string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.m[sender]; t != nil {
		return t.Dups()
	}
	return 0
}
