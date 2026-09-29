// Package preagg implements a two-stage pre-aggregator for skewed keys.
//
// Change events for the same key are first buffered locally by count. Once a
// key's buffered count reaches the configured threshold, the pending delta is
// pushed to the global view as a single change and the key is marked hot. A
// key occurring two or more times within one batch becomes hot after that
// batch ends; hot keys push every subsequent event individually.
package preagg

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Distinct, decidable rejection reasons for invalid input.
var (
	ErrNonPositiveThreshold = errors.New("preagg: threshold must be positive")
	ErrEmptyBatch           = errors.New("preagg: batch must contain at least one event")
	ErrEmptyKey             = errors.New("preagg: event key must not be empty")
	ErrZeroDelta            = errors.New("preagg: event delta must not be zero")
)

// Event is a single change to a key's accumulated value.
type Event struct {
	Key   string
	Delta int64
}

// PushRecord is one change pushed from the local buffer to the global view.
type PushRecord struct {
	Seq   int
	Key   string
	Delta int64
}

// BufferState is the local, not-yet-pushed state of one key.
type BufferState struct {
	Sum   int64
	Count int
}

// Aggregator is a two-stage pre-aggregator. It is safe for concurrent use:
// writers are serialized and readers always observe a consistent snapshot.
type Aggregator struct {
	mu        sync.RWMutex
	threshold int
	buffers   map[string]BufferState
	hot       map[string]struct{}
	view      map[string]int64
	fed       map[string]int64
	log       []PushRecord
}

// New returns an Aggregator with the given per-key buffering threshold.
func New(threshold int) (*Aggregator, error) {
	if threshold <= 0 {
		return nil, fmt.Errorf("%w, got %d", ErrNonPositiveThreshold, threshold)
	}
	return &Aggregator{
		threshold: threshold,
		buffers:   make(map[string]BufferState),
		hot:       make(map[string]struct{}),
		view:      make(map[string]int64),
		fed:       make(map[string]int64),
	}, nil
}

// AddBatch validates the whole batch and, only if every event is valid,
// applies it atomically. If any event is invalid the whole batch is rejected
// and no state is mutated.
func (a *Aggregator) AddBatch(events []Event) error {
	if len(events) == 0 {
		return ErrEmptyBatch
	}
	for i, ev := range events {
		if ev.Key == "" {
			return fmt.Errorf("%w (event %d)", ErrEmptyKey, i)
		}
		if ev.Delta == 0 {
			return fmt.Errorf("%w (event %d, key %q)", ErrZeroDelta, i, ev.Key)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	occurrences := make(map[string]int)
	for _, ev := range events {
		occurrences[ev.Key]++
		a.fed[ev.Key] += ev.Delta
		if _, isHot := a.hot[ev.Key]; isHot {
			a.pushLocked(ev.Key, ev.Delta)
			continue
		}
		buf := a.buffers[ev.Key]
		buf.Sum += ev.Delta
		buf.Count++
		if buf.Count >= a.threshold {
			a.pushLocked(ev.Key, buf.Sum)
			delete(a.buffers, ev.Key)
			a.hot[ev.Key] = struct{}{}
		} else {
			a.buffers[ev.Key] = buf
		}
	}
	for key, n := range occurrences {
		if n >= 2 {
			a.hot[key] = struct{}{}
		}
	}
	return nil
}

// FlushKey pushes the key's pending delta (if any) and removes it from the
// hot set. It reports whether a push happened.
func (a *Aggregator) FlushKey(key string) (PushRecord, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.hot, key)
	buf, ok := a.buffers[key]
	if !ok {
		return PushRecord{}, false
	}
	rec := a.pushLocked(key, buf.Sum)
	delete(a.buffers, key)
	return rec, true
}

// FlushAll pushes every key with a pending delta in lexicographic key order
// and clears the hot set. It returns the pushed records in push order.
func (a *Aggregator) FlushAll() []PushRecord {
	a.mu.Lock()
	defer a.mu.Unlock()

	keys := make([]string, 0, len(a.buffers))
	for key := range a.buffers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pushed := make([]PushRecord, 0, len(keys))
	for _, key := range keys {
		pushed = append(pushed, a.pushLocked(key, a.buffers[key].Sum))
		delete(a.buffers, key)
	}
	a.hot = make(map[string]struct{})
	return pushed
}

// pushLocked appends one change to the global view and the push log.
// The caller must hold a.mu.
func (a *Aggregator) pushLocked(key string, delta int64) PushRecord {
	rec := PushRecord{Seq: len(a.log), Key: key, Delta: delta}
	a.log = append(a.log, rec)
	a.view[key] += delta
	return rec
}

// View returns a copy of the global view (pushed deltas only).
func (a *Aggregator) View() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make(map[string]int64, len(a.view))
	for key, sum := range a.view {
		out[key] = sum
	}
	return out
}

// Pending returns a copy of the local, not-yet-pushed buffers.
func (a *Aggregator) Pending() map[string]BufferState {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make(map[string]BufferState, len(a.buffers))
	for key, buf := range a.buffers {
		out[key] = buf
	}
	return out
}

// HotKeys returns the current hot keys in lexicographic order.
func (a *Aggregator) HotKeys() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]string, 0, len(a.hot))
	for key := range a.hot {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// PushLog returns a copy of all push records in push order.
func (a *Aggregator) PushLog() []PushRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]PushRecord, len(a.log))
	copy(out, a.log)
	return out
}

// SelfCheck verifies the internal invariants and returns a descriptive
// error if any is violated:
//   - replaying the push log from empty exactly rebuilds the global view;
//   - for every key, view + pending buffer equals the sum of all accepted
//     event deltas fed so far;
//   - every buffered count is within [1, threshold).
func (a *Aggregator) SelfCheck() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	replayed := make(map[string]int64, len(a.view))
	for i, rec := range a.log {
		if rec.Seq != i {
			return fmt.Errorf("push log out of order at index %d: seq %d", i, rec.Seq)
		}
		replayed[rec.Key] += rec.Delta
	}
	for key, sum := range a.view {
		if replayed[key] != sum {
			return fmt.Errorf("view[%q]=%d does not match replayed push log sum %d", key, sum, replayed[key])
		}
	}
	for key, sum := range replayed {
		if _, ok := a.view[key]; !ok {
			return fmt.Errorf("push log replays key %q (sum %d) missing from view", key, sum)
		}
	}

	keys := make(map[string]struct{}, len(a.fed)+len(a.view)+len(a.buffers))
	for key := range a.fed {
		keys[key] = struct{}{}
	}
	for key := range a.view {
		keys[key] = struct{}{}
	}
	for key := range a.buffers {
		keys[key] = struct{}{}
	}
	for key := range keys {
		total := a.view[key] + a.buffers[key].Sum
		if total != a.fed[key] {
			return fmt.Errorf("key %q: view(%d)+pending(%d)=%d, want fed sum %d",
				key, a.view[key], a.buffers[key].Sum, total, a.fed[key])
		}
	}

	for key, buf := range a.buffers {
		if buf.Count < 1 || buf.Count >= a.threshold {
			return fmt.Errorf("key %q: buffered count %d outside [1, threshold %d)", key, buf.Count, a.threshold)
		}
	}
	return nil
}
