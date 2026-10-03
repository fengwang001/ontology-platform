// Package span defines the per-trace buffering entry used by the tail sampler.
// It is pure data: no policy, quota or cache knowledge lives here.
package span

// Entry accumulates the spans of one trace while it sits in the buffer.
type Entry struct {
	// LastSeen is the clock value of the most recently accepted span.
	LastSeen uint64

	seenErr bool
	maxDur  uint64
	ids     map[string]struct{}
}

// New returns an empty Entry ready to accept spans.
func New() *Entry {
	return &Entry{ids: make(map[string]struct{})}
}

// Has reports whether spanID was already added to this entry.
func (e *Entry) Has(spanID string) bool {
	_, ok := e.ids[spanID]
	return ok
}

// Add records one span. Callers must guarantee spanID is not a duplicate.
func (e *Entry) Add(spanID string, durMs uint64, isErr bool) {
	e.ids[spanID] = struct{}{}
	if durMs > e.maxDur {
		e.maxDur = durMs
	}
	if isErr {
		e.seenErr = true
	}
}

// Count returns the number of distinct spans accumulated so far.
func (e *Entry) Count() int {
	return len(e.ids)
}

// SeenErr reports whether any span carried the error flag.
func (e *Entry) SeenErr() bool {
	return e.seenErr
}

// MaxDur returns the maximum durMs observed across all spans.
func (e *Entry) MaxDur() uint64 {
	return e.maxDur
}
