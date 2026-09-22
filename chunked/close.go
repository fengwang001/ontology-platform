package chunked

import (
	"ontology/internal/frame"
	"ontology/internal/hexline"
)

// Close signals that no further bytes will arrive. It returns nil for a
// complete message and a distinguishable *Error naming the state in which
// the stream was cut off otherwise. After a protocol error Close returns
// that same error.
func (d *Decoder) Close() error {
	if d.term != nil {
		if d.term.Kind == KindAlreadyDone {
			return nil
		}
		return d.term
	}
	if d.ph == phComplete {
		return nil
	}

	var kind Kind
	switch d.ph {
	case phTrailers:
		kind = KindIncompleteTrailers
	case phChunkHeader:
		if d.fr != nil {
			if he, ok := hexline.AsError(d.fr.HeaderClose()); ok &&
				he.Kind == hexline.KindUnterminatedQuote {
				d.term = &Error{Kind: KindUnterminatedQuote, Offset: d.stream}
				return d.term
			}
		}
		kind = KindIncompleteHeader
	case phChunkData:
		kind = KindIncompleteData
	case phChunkCRLF:
		if d.fr != nil {
			if fe, ok := frame.AsError(d.fr.Close()); ok && fe.CRSeen {
				kind = KindHalfCRLF
				break
			}
		}
		kind = KindIncompleteCRLF
	default:
		kind = KindIncompleteHeader
	}
	d.term = &Error{Kind: kind, Offset: d.stream}
	return d.term
}
