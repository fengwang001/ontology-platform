// Package local is the dependency-free per-key write buffer of the two-stage
// pre-aggregator: count-based coalescing, threshold pushes and hot-key marks.
package local

import (
	"errors"
	"sort"
)

// Event is one upstream change and also the unit pushed downstream.
type Event struct {
	Key   string
	Delta int64
}

// Sentinel errors: callers judge failures with errors.Is.
var (
	ErrEmptyBatch = errors.New("local: empty event batch")
	ErrEmptyKey   = errors.New("local: empty event key")
	ErrZeroDelta  = errors.New("local: zero event delta")
)

type entry struct {
	sum int64
	n   int64
}

// Buffer is the per-key local buffer. Use NewBuffer; the zero value is not set up.
type Buffer struct {
	h       int64
	pending map[string]*entry
	hot     map[string]struct{}
	// probe is how many keys the most recent threshold-triggered push
	// inspected. A direct map hit inspects one key; a table scan would grow
	// with the buffer size. It stays unexported and is read only by white-box
	// tests; no exported method ever reports its value.
	probe int
}

// NewBuffer creates a Buffer with event-count threshold H (H > 0).
func NewBuffer(H int64) *Buffer {
	return &Buffer{h: H, pending: map[string]*entry{}, hot: map[string]struct{}{}}
}

// Feed applies one batch and returns its pushes in push order. The batch is
// validated in full before any state change, so a rejected batch leaves no
// trace. In-batch repetition is judged only after the whole batch is seen, so
// a newly marked hot key is immediate-pushed from the next batch onwards.
func (b *Buffer) Feed(evs []Event) ([]Event, error) {
	if len(evs) == 0 {
		return nil, ErrEmptyBatch
	}
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
		if e.Delta == 0 {
			return nil, ErrZeroDelta
		}
	}
	seen := make(map[string]int64)
	out := make([]Event, 0)
	for _, e := range evs {
		seen[e.Key]++
		if _, hot := b.hot[e.Key]; hot {
			out = append(out, e) // hot key: each event is pushed alone at once
			continue
		}
		en := b.pending[e.Key] // direct map positioning, never a table scan
		if en == nil {
			en = &entry{}
			b.pending[e.Key] = en
		}
		en.sum += e.Delta
		en.n++
		if en.n >= b.h {
			b.probe = 1 // this trigger inspected only the key located above
			out = append(out, Event{Key: e.Key, Delta: en.sum})
			delete(b.pending, e.Key)
			b.hot[e.Key] = struct{}{} // threshold push marks the key hot at once
		}
	}
	for k, c := range seen {
		if c >= 2 { // repetition judged on the full, already observed batch
			b.hot[k] = struct{}{}
		}
	}
	return out, nil
}

// FlushKey pushes the buffered sum of k when present and unmarks k as hot.
func (b *Buffer) FlushKey(k string) []Event {
	out := make([]Event, 0, 1)
	if e, ok := b.pending[k]; ok {
		out = append(out, Event{Key: k, Delta: e.sum})
		delete(b.pending, k)
	}
	delete(b.hot, k)
	return out
}

// FlushAll pushes every key with a buffered sum in lexicographic order and
// removes the pushed keys from the hot set.
func (b *Buffer) FlushAll() []Event {
	keys := make([]string, 0, len(b.pending))
	for k := range b.pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Event, 0, len(keys))
	for _, k := range keys {
		out = append(out, Event{Key: k, Delta: b.pending[k].sum})
		delete(b.pending, k)
		delete(b.hot, k)
	}
	return out
}

// PendingAll returns a copy of all buffered sums.
func (b *Buffer) PendingAll() map[string]int64 {
	m := make(map[string]int64, len(b.pending))
	for k, e := range b.pending {
		m[k] = e.sum
	}
	return m
}

// Hot returns the hot keys in lexicographic order.
func (b *Buffer) Hot() []string {
	ks := make([]string, 0, len(b.hot))
	for k := range b.hot {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
