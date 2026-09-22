package frame

import "ontology/internal/hexline"

// Phase is the point at which a frame Reader currently waits for input.
type Phase int

const (
	// PhaseHeader: reading the chunk-size line.
	PhaseHeader Phase = iota
	// PhaseData: reading chunk data bytes.
	PhaseData
	// PhaseCRLF: reading the CRLF that follows chunk data.
	PhaseCRLF
	// PhaseDone: the whole chunk, including CRLF, was consumed.
	PhaseDone
)

// Reader incrementally consumes exactly one chunk: size line, size data
// bytes, and a trailing CRLF. Input may be split at any byte boundary.
//
// Reader is not safe for concurrent use.
type Reader struct {
	hp *hexline.Parser

	phase Phase
	size  uint64
	got   uint64 // data bytes consumed
	off   int    // total bytes consumed by this frame

	cr bool // CR seen while in PhaseCRLF

	// Sink receives each chunk-data byte; returning an error aborts the frame.
	Sink func(b byte) error
	// OnSize is called once, with the declared chunk size, when the
	// size line is complete. A non-nil result aborts the frame.
	OnSize func(size uint64) error
}

// NewReader returns a Reader whose size line must not exceed maxLine bytes.
func NewReader(maxLine int) *Reader {
	return &Reader{hp: hexline.NewParser(maxLine), phase: PhaseHeader}
}

// Phase returns the reader's current position in the frame.
func (r *Reader) Phase() Phase { return r.phase }

// Size reports the declared chunk size once the size line is complete.
func (r *Reader) Size() uint64 { return r.size }

// Done reports whether the entire chunk including its trailing CRLF was read.
func (r *Reader) Done() bool { return r.phase == PhaseDone }

// Feed consumes bytes from in, returning how many were used. It stops when
// the frame is complete or in is exhausted. Every consumed input byte is
// accounted for exactly once; a non-nil error leaves the Reader terminal.
func (r *Reader) Feed(in []byte) (int, error) {
	consumed := 0
	for consumed < len(in) {
		switch r.phase {
		case PhaseHeader:
			n, size, err := r.hp.Feed(in[consumed:])
			consumed += n
			r.off += n
			if err != nil {
				return consumed, fromHex(err)
			}
			if r.hp.Done() {
				r.size = size
				if size == 0 {
					r.phase = PhaseDone
					return consumed, nil
				}
				r.phase = PhaseData
				if r.OnSize != nil {
					if err := r.OnSize(size); err != nil {
						return consumed, err
					}
				}
			}
		case PhaseData:
			avail := uint64(len(in) - consumed)
			take := r.size - r.got
			if take > avail {
				take = avail
			}
			for i := uint64(0); i < take; i++ {
				if r.Sink != nil {
					if err := r.Sink(in[consumed]); err != nil {
						return consumed, err
					}
				}
				consumed++
				r.off++
				r.got++
			}
			if r.got == r.size {
				r.phase = PhaseCRLF
			}
		case PhaseCRLF:
			c := in[consumed]
			if !r.cr {
				if c != '\r' {
					return consumed, &Error{Kind: KindMissingCRLF, Offset: r.off}
				}
				r.cr = true
				consumed++
				r.off++
			} else {
				if c != '\n' {
					return consumed, &Error{Kind: KindMissingCRLF, Offset: r.off}
				}
				consumed++
				r.off++
				r.phase = PhaseDone
				return consumed, nil
			}
		case PhaseDone:
			return consumed, nil
		}
	}
	return consumed, nil
}

// Close reports whether the frame was truncated. The returned *Error, if
// any, describes the phase in which input ran out.
func (r *Reader) Close() error {
	switch r.phase {
	case PhaseDone:
		return nil
	case PhaseCRLF:
		return &Error{Kind: KindMissingCRLF, Offset: r.off, CRSeen: r.cr}
	default:
		return fromHex(r.hp.Close())
	}
}

// HeaderClose returns the underlying size-line parser's end-of-stream error.
func (r *Reader) HeaderClose() error { return r.hp.Close() }
