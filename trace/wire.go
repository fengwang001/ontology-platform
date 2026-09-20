package trace

import (
	"slices"
	"strings"
)

// Wire format:
//
//	<traceID>-<spanID>-<parentID>-<01|00>[;key=value]...
//
// The parent ID field is empty for root spans; it is carried on the
// wire so that a full round trip through Marshal and Parse preserves
// the derivation chain. Baggage items are appended in ascending key
// order, which makes Marshal output deterministic.

// Marshal encodes the span into its wire representation. The result
// is byte-for-byte stable across calls for the same span state.
func (s *Span) Marshal() string {
	s.mu.RLock()
	bag := make(map[string]string, len(s.baggage))
	for k, v := range s.baggage {
		bag[k] = v
	}
	s.mu.RUnlock()

	keys := make([]string, 0, len(bag))
	for k := range bag {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	b.WriteString(string(s.traceID))
	b.WriteByte('-')
	b.WriteString(string(s.spanID))
	b.WriteByte('-')
	b.WriteString(string(s.parentID))
	b.WriteByte('-')
	if s.sampled {
		b.WriteString("01")
	} else {
		b.WriteString("00")
	}
	for _, k := range keys {
		b.WriteByte(';')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(bag[k])
	}
	return b.String()
}

// Parse decodes a wire representation into a new Span. On any format
// violation it returns nil and ErrMalformed; the returned span never
// shares mutable state with any other span.
func Parse(wire string) (*Span, error) {
	parts := strings.Split(wire, ";")
	fields := strings.Split(parts[0], "-")
	if len(fields) != 4 {
		return nil, ErrMalformed
	}
	traceID, spanID, parentID, flags := fields[0], fields[1], fields[2], fields[3]
	if traceID == "" || spanID == "" {
		return nil, ErrMalformed
	}
	var sampled bool
	switch flags {
	case "01":
		sampled = true
	case "00":
	default:
		return nil, ErrMalformed
	}

	bag := make(map[string]string, len(parts)-1)
	for _, item := range parts[1:] {
		i := strings.Index(item, "=")
		if i <= 0 {
			return nil, ErrMalformed
		}
		bag[item[:i]] = item[i+1:]
	}

	return &Span{
		traceID:  SpanID(traceID),
		spanID:   SpanID(spanID),
		parentID: SpanID(parentID),
		sampled:  sampled,
		newID:    lockedGen(randomID),
		baggage:  bag,
	}, nil
}
