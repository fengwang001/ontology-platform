package trace

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Span is a node in a trace tree. It is safe for concurrent use.
type Span struct {
	traceID  SpanID
	spanID   SpanID
	parentID SpanID
	sampled  bool

	newID func() SpanID

	mu      sync.RWMutex
	baggage map[string]string
}

// NewRoot creates a root span. newID is the injectable ID generator
// used for this span and all spans derived from it. The generator is
// wrapped so that concurrent derivation anywhere in the tree is
// serialized, even if newID itself is not goroutine-safe. If newID is
// nil, a random generator is used. Generated IDs must not contain
// '-' or ';', the wire format separators.
func NewRoot(newID func() SpanID, sampled bool) *Span {
	if newID == nil {
		newID = randomID
	}
	gen := lockedGen(newID)
	id := gen()
	return &Span{
		traceID: id,
		spanID:  id,
		sampled: sampled,
		newID:   gen,
		baggage: make(map[string]string),
	}
}

// lockedGen wraps an ID generator so concurrent calls are serialized,
// even if the generator itself is not goroutine-safe.
func lockedGen(newID func() SpanID) func() SpanID {
	var mu sync.Mutex
	return func() SpanID {
		mu.Lock()
		defer mu.Unlock()
		return newID()
	}
}

// Child derives a child span from s. The child shares the trace ID and
// the sampling decision, gets a fresh span ID from the generator, and
// receives a copy of the baggage present at derivation time.
func (s *Span) Child() *Span {
	id := s.newID()
	s.mu.RLock()
	bag := make(map[string]string, len(s.baggage))
	for k, v := range s.baggage {
		bag[k] = v
	}
	s.mu.RUnlock()
	return &Span{
		traceID:  s.traceID,
		spanID:   id,
		parentID: s.spanID,
		sampled:  s.sampled,
		newID:    s.newID,
		baggage:  bag,
	}
}

// TraceID returns the ID shared by the whole trace tree.
func (s *Span) TraceID() SpanID {
	return s.traceID
}

// SpanID returns this span's own ID.
func (s *Span) SpanID() SpanID {
	return s.spanID
}

// ParentID returns the parent's span ID, empty for a root.
func (s *Span) ParentID() SpanID {
	return s.parentID
}

// Sampled reports the sampling decision inherited at derivation.
func (s *Span) Sampled() bool {
	return s.sampled
}

func randomID() SpanID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return SpanID(hex.EncodeToString(b[:]))
}
