// Package fifo holds the per-key reorder-buffer state for a single key.
// It depends on no other package in this module.
package fifo

import (
	"errors"
	"sort"
)

// Sentinel errors; callers must use errors.Is to classify them.
var (
	ErrInvalidMax   = errors.New("fifo: maxInFlight must be positive")
	ErrBackpressure = errors.New("fifo: in-flight buffer would exceed maxInFlight")
)

// KeyState is the state for one key: next seq to emit, a sorted buffer of
// future seqs, the emitted prefix and counters.
type KeyState struct {
	next    int64
	max     int
	buf     []int64 // accepted future seqs, kept sorted ascending; live range starts at head
	head    int
	emitted []int64
	dropped int64

	// probes counts buffer entries inspected while deciding whether the
	// buffer contains `next` during the most recent Feed. Unexported on
	// purpose: it must never leave the package via any exported API.
	probes int64
}

// New creates one key's state with the given in-flight bound.
func New(maxInFlight int) (*KeyState, error) {
	if maxInFlight <= 0 {
		return nil, ErrInvalidMax
	}
	return &KeyState{next: 1, max: maxInFlight}, nil
}

// Feed delivers one seq. It returns the seqs newly emitted by this call
// (always a consecutive run). A duplicate is dropped and reports (nil, nil).
func (k *KeyState) Feed(seq int64) ([]int64, error) {
	k.probes = 0
	switch {
	case seq < k.next:
		// Already emitted: idempotent duplicate.
		k.dropped++
		return nil, nil
	case seq == k.next:
		out := []int64{seq}
		k.emitted = append(k.emitted, seq)
		k.next++
		k.cascade(&out)
		return out, nil
	default: // seq > next: buffer it, but reject first and leave no trace.
		i := sort.Search(k.size(), func(i int) bool { return k.buf[k.head+i] > seq })
		if i > 0 && k.buf[k.head+i-1] == seq {
			return nil, nil // duplicate of an already-buffered future seq: idempotent
		}
		if k.size() >= k.max {
			return nil, ErrBackpressure
		}
		k.buf = append(k.buf, 0)
		copy(k.buf[k.head+i+1:], k.buf[k.head+i:])
		k.buf[k.head+i] = seq
		return nil, nil
	}
}

// cascade releases the sorted consecutive prefix of the buffer starting at
// next. Only the single head entry is inspected per release, so probes is
// linear in the number released rather than quadratic.
func (k *KeyState) cascade(out *[]int64) {
	for k.head < len(k.buf) {
		k.probes++
		s := k.buf[k.head]
		if s != k.next {
			break // gap ahead: stop, nothing beyond the head can be consecutive
		}
		*out = append(*out, s)
		k.emitted = append(k.emitted, s)
		k.next++
		k.head++
	}
	// Compact at most once per cascade and only when at least half the
	// backing array is dead space, so the copy cost is amortized O(1)
	// per released entry.
	if k.head > 0 && k.head*2 >= len(k.buf) {
		k.buf = append(make([]int64, 0, len(k.buf)-k.head), k.buf[k.head:]...)
		k.head = 0
	}
}

func (k *KeyState) size() int { return len(k.buf) - k.head }

// Emitted returns a copy of the prefix emitted so far (1,2,...,next-1).
func (k *KeyState) Emitted() []int64 {
	out := make([]int64, len(k.emitted))
	copy(out, k.emitted)
	return out
}

// Buffered returns the number of buffered future seqs.
func (k *KeyState) Buffered() int { return k.size() }

// Dropped returns the number of duplicate deliveries (seq < next).
func (k *KeyState) Dropped() int64 { return k.dropped }

// SelfCheck runs built-in scenarios, including the probe-bound check across
// several m values. It reports only pass/fail, never the probe count itself.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		k, err := New(m + 1)
		if err != nil {
			return err
		}
		for s := int64(m + 1); s >= 2; s-- { // seqs next+1..next+m arrive early, out of order
			if _, err := k.Feed(s); err != nil {
				return err
			}
		}
		if _, err := k.Feed(1); err != nil {
			return err
		}
		if k.probes > int64(m)+4 {
			return errors.New("fifo: self-check probe bound violated")
		}
		if len(k.emitted) != m+1 || k.Buffered() != 0 {
			return errors.New("fifo: self-check cascade incomplete")
		}
	}
	return nil
}
