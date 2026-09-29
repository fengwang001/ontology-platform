// Package ontology implements an all-or-nothing atomic batch materializer.
//
// A batch is a sequence of Events. Each event writes or deletes one key and
// carries a precondition (nil = key must be absent, non-nil = current value
// must equal it exactly). Events are rehearsed in batch order against the
// committed view overlaid with earlier events of the same batch. The first
// event whose precondition fails rejects the whole batch; otherwise the
// resulting view is published in a single atomic pointer swap. Readers
// therefore observe either the complete before-batch view or the complete
// after-batch view, never an intermediate state.
package ontology

import (
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
)

// Op describes the kind of mutation an Event performs on a key.
type Op int

const (
	// Write sets a key to the event's Value.
	Write Op = iota + 1
	// Delete removes a key from the visible view.
	Delete
)

// Event is one mutation of a key with a precondition.
// A nil Expect means the key must currently be absent; a non-nil Expect
// means the current value must equal *Expect byte-for-byte.
type Event struct {
	Key    string
	Op     Op
	Value  string // used only when Op == Write
	Expect *string
}

// Materializer applies batches of key-value mutations atomically.
//
// Apply calls are serialized; Get, Snapshot and Check are lock-free and may
// run concurrently with each other and with Apply.
type Materializer struct {
	view atomic.Pointer[map[string]string]
	gen  atomic.Uint64

	applyMu sync.Mutex

	logger *log.Logger
}

// Option customizes a Materializer at construction.
type Option func(*Materializer)

// WithLogger routes the materializer's audit logs to w. Pass nil to disable
// logging. By default the standard logger is used.
func WithLogger(w io.Writer) Option {
	return func(m *Materializer) {
		if w == nil {
			m.logger = nil
			return
		}
		m.logger = log.New(w, "ontology: ", log.LstdFlags|log.Lmicroseconds)
	}
}

// New returns an empty Materializer.
func New(opts ...Option) *Materializer {
	m := &Materializer{
		logger: log.Default(),
	}
	empty := map[string]string{}
	m.view.Store(&empty)
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ErrCorrupt is returned by Check when the internal view invariant breaks.
var ErrCorrupt = errors.New("ontology: materializer internal state is corrupt")

// Apply rehearses the batch in order and, only if every event satisfies its
// precondition, publishes the resulting view in one atomic step.
//
// On failure a *BatchError describing the first failing event is returned
// and no visible state changes.
func (m *Materializer) Apply(batch []Event) error {
	m.logf("apply begin: n=%d events=%s", len(batch), formatBatch(batch))

	if len(batch) == 0 {
		m.logf("apply rejected: %v (no state change)", ErrEmptyBatch)
		return ErrEmptyBatch
	}

	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	// Rehearse on a private working copy. It is only published after every
	// event succeeds, so a rejected batch leaves the committed view untouched.
	committed := m.view.Load()
	working := make(map[string]string, len(*committed)+len(batch))
	for k, v := range *committed {
		working[k] = v
	}

	for i, event := range batch {
		if event.Key == "" {
			err := &BatchError{Index: i, Key: event.Key, Op: event.Op, Reason: ErrEmptyKey}
			m.logf("apply rejected at event %d: %v (no state change)", i, err)
			return err
		}
		if event.Op != Write && event.Op != Delete {
			err := &BatchError{Index: i, Key: event.Key, Op: event.Op, Reason: ErrInvalidOp, Expected: event.Expect}
			m.logf("apply rejected at event %d: %v (no state change)", i, err)
			return err
		}

		observed, found := working[event.Key]
		var actual *string
		if found {
			v := observed
			actual = &v
		}

		matched := false
		switch {
		case event.Expect == nil:
			matched = !found
		default:
			matched = found && observed == *event.Expect
		}
		m.logf("rehearse event %d: key=%q op=%s expect=%s observed=%s match=%t",
			i, event.Key, event.Op, formatExpect(event.Expect), formatExpect(actual), matched)

		if !matched {
			err := &BatchError{
				Index:    i,
				Key:      event.Key,
				Op:       event.Op,
				Reason:   ErrPrecondition,
				Expected: event.Expect,
				Actual:   actual,
			}
			m.logf("apply rejected at event %d: %v expected=%s actual=%s (no state change)",
				i, ErrPrecondition, formatExpect(event.Expect), formatExpect(actual))
			return err
		}

		switch event.Op {
		case Write:
			working[event.Key] = event.Value
		case Delete:
			delete(working, event.Key)
		}
	}

	// Single publication point: the whole after-batch view appears at once.
	published := working
	m.view.Store(&published)
	m.gen.Add(1)
	m.logf("apply committed: generation=%d view-size=%d", m.gen.Load(), len(published))
	return nil
}

// Get reports the value of a key in the current committed view. The read
// corresponds to some complete batch boundary.
func (m *Materializer) Get(key string) (value string, ok bool) {
	view := m.view.Load()
	value, ok = (*view)[key]
	return
}

// Snapshot returns a point-in-time copy of the committed view. The copy
// corresponds to one complete batch boundary and may be mutated freely.
func (m *Materializer) Snapshot() map[string]string {
	src := m.view.Load()
	dst := make(map[string]string, len(*src))
	for k, v := range *src {
		dst[k] = v
	}
	return dst
}

// Generation returns the number of committed batches so far. It increases
// by exactly one per successful Apply.
func (m *Materializer) Generation() uint64 {
	return m.gen.Load()
}

// Check self-verifies the invariant that a visible view always corresponds
// to a complete batch boundary. It is safe for concurrent use.
func (m *Materializer) Check() error {
	v := m.view.Load()
	if v == nil {
		return ErrCorrupt
	}
	return nil
}

func (m *Materializer) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}
