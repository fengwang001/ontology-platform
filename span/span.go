// Package span holds the per-trace buffer state used by the tail sampler.
package span

// Entry is the accumulated state of one buffered trace.
type Entry struct {
	traceID  string
	lastSeen int64
	spans    int64
	seen     map[string]struct{}
	hasError bool
	maxDur   int64
}

// New creates an empty buffer entry for traceID.
func New(traceID string) *Entry {
	return &Entry{traceID: traceID, seen: make(map[string]struct{})}
}

// Add appends one span; it returns false when spanID is already present.
func (e *Entry) Add(spanID string, now, durMs int64, isErr bool) bool {
	if _, dup := e.seen[spanID]; dup {
		return false
	}
	e.seen[spanID] = struct{}{}
	e.spans++
	e.lastSeen = now
	if durMs > e.maxDur {
		e.maxDur = durMs
	}
	if isErr {
		e.hasError = true
	}
	return true
}

// TraceID returns the trace identifier.
func (e *Entry) TraceID() string { return e.traceID }

// LastSeen returns the timestamp of the most recently accepted span.
func (e *Entry) LastSeen() int64 { return e.lastSeen }

// Spans returns the number of accepted spans in this trace.
func (e *Entry) Spans() int64 { return e.spans }

// HasError reports whether any accepted span carried an error flag.
func (e *Entry) HasError() bool { return e.hasError }

// MaxDur returns the maximum span duration observed so far.
func (e *Entry) MaxDur() int64 { return e.maxDur }
