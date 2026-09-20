package trace

import (
	"errors"
	"strings"
)

// ErrMalformed is returned by Parse when the wire format is invalid.
var ErrMalformed = errors.New("trace: malformed wire format")

// Wire format:
//
//	<traceID>-<spanID>-<01|00>[-<key>=<value>]...
//
// Baggage items are appended in ascending key order, so Marshal is
// deterministic: repeated calls on the same span produce byte-identical
// strings regardless of map iteration order.

const (
	flagSampled    = "01"
	flagNotSampled = "00"
)

// Marshal encodes the span in the wire format. Baggage is emitted in
// ascending key order, making the output deterministic.
func (s *Span) Marshal() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString(string(s.traceID))
	sb.WriteByte('-')
	sb.WriteString(string(s.spanID))
	sb.WriteByte('-')
	if s.sampled {
		sb.WriteString(flagSampled)
	} else {
		sb.WriteString(flagNotSampled)
	}
	for _, k := range sortedKeys(s.baggage) {
		sb.WriteByte('-')
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(s.baggage[k])
	}
	return sb.String()
}

// Parse decodes a wire-format string into a new span. On any
// malformed input it returns (nil, ErrMalformed); a half-constructed
// span is never returned. A parsed span owns a fresh baggage map and
// shares no mutable state with the span the string came from. Its ID
// generator is the default random one, since a generator cannot be
// recovered from the wire.
func Parse(wire string) (*Span, error) {
	fields := strings.Split(wire, "-")
	if len(fields) < 3 {
		return nil, ErrMalformed
	}
	traceID, spanID := fields[0], fields[1]
	if traceID == "" || spanID == "" {
		return nil, ErrMalformed
	}
	var sampled bool
	switch fields[2] {
	case flagSampled:
		sampled = true
	case flagNotSampled:
	default:
		return nil, ErrMalformed
	}
	bag := make(map[string]string, len(fields)-3)
	for _, item := range fields[3:] {
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			return nil, ErrMalformed
		}
		bag[k] = v
	}
	return &Span{
		traceID: SpanID(traceID),
		spanID:  SpanID(spanID),
		sampled: sampled,
		baggage: bag,
		newID:   randomID,
	}, nil
}
