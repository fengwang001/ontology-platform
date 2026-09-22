package chunked

import (
	"ontology/internal/frame"
)

type phase int

const (
	phChunkHeader phase = iota
	phChunkData
	phChunkCRLF
	phTrailers
	phComplete
)

// Decoder is an incremental chunked-transfer decoder. Feed bytes with Write
// and call Close when the stream has ended. It is not safe for concurrent
// use; share independent Decoders between goroutines instead.
type Decoder struct {
	limits Limits

	ph     phase
	total  int64 // bytes decoded so far
	stream int   // bytes consumed from the concatenated input stream

	fr *frame.Reader

	// trailer section state
	tCount int  // trailer lines seen
	tCR    bool // CR seen on the current line
	tEmpty bool // current line is empty so far

	body []byte

	term *Error // terminal error, sticky after first occurrence
}

// Write consumes as much of p as possible and returns the number of bytes
// consumed. The returned error is non-nil exactly when the decoder reaches a
// terminal state; afterwards the same error is returned for every Write.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.term != nil {
		return 0, d.term
	}
	if d.ph == phComplete {
		d.term = &Error{Kind: KindAlreadyDone, Offset: d.stream}
		return 0, d.term
	}

	consumed := 0
	for consumed < len(p) {
		var n int
		var err error
		if d.ph == phTrailers {
			n, err = d.feedTrailers(p[consumed:])
		} else {
			n, err = d.feedChunk(p[consumed:])
		}
		consumed += n
		d.stream += n
		if err != nil {
			ce, ok := AsError(err)
			if !ok {
				ce = &Error{Kind: KindBadTrailer, Offset: d.stream}
			}
			d.term = ce
			return consumed, d.term
		}
		if n == 0 {
			break
		}
	}
	return consumed, nil
}

func (d *Decoder) ensureFrame() {
	if d.fr != nil {
		return
	}
	d.fr = frame.NewReader(d.limits.MaxSizeLine)
	d.fr.Sink = d.sink
	d.fr.OnSize = d.onSize
}

func (d *Decoder) feedChunk(in []byte) (int, error) {
	d.ensureFrame()
	n, err := d.fr.Feed(in)
	if err != nil {
		if ce, ok := AsError(err); ok {
			ce.Offset += d.stream
			return n, ce
		}
		return n, mapFrameError(err)
	}
	switch d.fr.Phase() {
	case frame.PhaseHeader:
		d.ph = phChunkHeader
	case frame.PhaseData:
		d.ph = phChunkData
	case frame.PhaseCRLF:
		d.ph = phChunkCRLF
	}
	if d.fr.Done() {
		if d.fr.Size() == 0 {
			d.tEmpty = true // trailer section starts on a fresh empty line
			d.ph = phTrailers
		} else {
			d.fr = nil
		}
	}
	return n, nil
}

func (d *Decoder) onSize(size uint64) error {
	if d.limits.MaxChunk > 0 && int64(size) > d.limits.MaxChunk {
		return &Error{Kind: KindChunkTooLarge, Offset: d.stream}
	}
	if d.limits.MaxBody > 0 && int64(size) > d.limits.MaxBody-d.total {
		return &Error{Kind: KindBodyTooLarge, Offset: d.stream}
	}
	return nil
}

func (d *Decoder) sink(b byte) error {
	d.total++
	if d.limits.MaxBody > 0 && d.total > d.limits.MaxBody {
		d.total--
		return &Error{Kind: KindBodyTooLarge, Offset: d.stream}
	}
	d.body = append(d.body, b)
	return nil
}

// Done reports whether the complete chunked message, including the trailer
// section's terminating blank line, was consumed.
func (d *Decoder) Done() bool { return d.ph == phComplete }

// Body returns the bytes decoded so far. The returned slice is a copy and is
// unaffected by later decoding (or by the decoder entering an error state).
func (d *Decoder) Body() []byte {
	out := make([]byte, len(d.body))
	copy(out, d.body)
	return out
}

// Phase returns a short name for the decoder's current internal state.
func (d *Decoder) Phase() string {
	switch d.ph {
	case phChunkHeader:
		return "size-line"
	case phChunkData:
		return "chunk-data"
	case phChunkCRLF:
		return "chunk-crlf"
	case phTrailers:
		return "trailers"
	default:
		return "complete"
	}
}
