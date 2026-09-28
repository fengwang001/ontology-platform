package sampler

import "sync"

// SeqRandom is a deterministic, caller-injected sequence of uniform random
// values. The sequence is fixed up front, which makes every run reproducible.
//
// The cursor exposes a peek/advance protocol: Peek inspects the next value
// without moving the cursor, and Advance moves it exactly once. A sampler that
// rejects an element only ever peeks, so rejected elements consume no random
// value. Values are consumed precisely when Advance is called.
type SeqRandom struct {
	mu     sync.Mutex
	values []float64
	cursor int
}

// NewSeqRandom validates the injected sequence and returns it. Every value must
// lie in the open interval (0, 1); otherwise the sequence is rejected as a
// whole and no sampler state exists yet.
func NewSeqRandom(values []float64) (*SeqRandom, error) {
	for i, u := range values {
		if !validUniform(u) {
			return nil, invalidRandomAt(i, u)
		}
	}
	cp := make([]float64, len(values))
	copy(cp, values)
	return &SeqRandom{values: cp}, nil
}

// Peek returns the next value without advancing the cursor.
func (r *SeqRandom) Peek() (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cursor >= len(r.values) {
		return 0, ErrRandomExhausted
	}
	return r.values[r.cursor], nil
}

// Advance consumes the peeked value. Advancing an exhausted source is a no-op.
func (r *SeqRandom) Advance() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cursor < len(r.values) {
		r.cursor++
	}
}

// Consumed reports the number of values already drawn.
func (r *SeqRandom) Consumed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cursor
}

// Remaining reports the number of values still available.
func (r *SeqRandom) Remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.values) - r.cursor
}

func (r *SeqRandom) snapshotCursor() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cursor
}

func (r *SeqRandom) restoreCursor(c int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c >= 0 && c <= len(r.values) {
		r.cursor = c
	}
}

func validUniform(u float64) bool {
	return u == u && u > 0 && u < 1 // reject NaN and values outside (0, 1)
}

type invalidRandomError struct {
	index int
	value float64
}

func (e invalidRandomError) Error() string {
	return "sampler: invalid random value"
}

func (e invalidRandomError) Unwrap() error { return ErrInvalidRandom }

func invalidRandomAt(index int, value float64) error {
	return invalidRandomError{index: index, value: value}
}
