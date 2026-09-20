// Package trace implements derivation and propagation of tracing
// contexts: span trees with inherited trace IDs, a sampling flag that
// is only ever inherited, copy-on-write baggage, and a deterministic
// wire format. It is not a wrapper around context.Context or any
// existing tracing library.
package trace

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// SpanID identifies a span (or, as a trace ID, the root of a tree).
// Because the wire format uses '-' as a field separator and '=' as
// the baggage key/value separator, IDs must not contain '-' or '=';
// the same holds for baggage keys and values.
type SpanID string

// Span is a node in a trace tree. The zero value is not usable;
// construct spans with NewRoot, Child or Parse.
//
// traceID, spanID, parentID and sampled are immutable after
// construction. baggage is mutable and guarded by mu; every Span owns
// its baggage map exclusively (copies are made on derivation and
// parsing), so no two spans ever share a map.
type Span struct {
	mu       sync.RWMutex
	traceID  SpanID
	spanID   SpanID
	parentID SpanID
	sampled  bool
	baggage  map[string]string
	newID    func() SpanID
}

// NewRoot creates a root span. newID is called once to produce the
// span's ID, which doubles as the trace ID; it is stored and reused
// for future Child calls. If newID is nil, a crypto-random generator
// is used. sampled is the sampling decision for the whole tree.
func NewRoot(newID func() SpanID, sampled bool) *Span {
	if newID == nil {
		newID = randomID
	}
	id := newID()
	return &Span{
		traceID: id,
		spanID:  id,
		sampled: sampled,
		baggage: make(map[string]string),
		newID:   newID,
	}
}

// Child derives a child span. The child inherits the trace ID, the
// sampling flag and a snapshot of the current baggage; it receives a
// fresh span ID from the parent's generator and records the parent's
// span ID as its parent. The baggage snapshot is a deep copy, so
// later writes on either side are invisible to the other.
//
// The generator is invoked while holding the parent's lock, so
// concurrent Child calls are serialized and each child gets its own
// ID from the generator's sequence.
func (s *Span) Child() *Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	bag := make(map[string]string, len(s.baggage))
	for k, v := range s.baggage {
		bag[k] = v
	}
	return &Span{
		traceID:  s.traceID,
		spanID:   s.newID(),
		parentID: s.spanID,
		sampled:  s.sampled,
		baggage:  bag,
		newID:    s.newID,
	}
}

// TraceID returns the ID of the root span of this tree.
func (s *Span) TraceID() SpanID { return s.traceID }

// SpanID returns this span's own ID.
func (s *Span) SpanID() SpanID { return s.spanID }

// ParentID returns the span ID of the parent, or "" for a root span.
func (s *Span) ParentID() SpanID { return s.parentID }

// Sampled reports the sampling decision inherited from the root.
func (s *Span) Sampled() bool { return s.sampled }

// randomID is the default ID generator (16 bytes of crypto-randomness,
// hex-encoded). It is only used when no generator is injected.
func randomID() SpanID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("trace: crypto/rand unavailable: " + err.Error())
	}
	return SpanID(hex.EncodeToString(b[:]))
}
