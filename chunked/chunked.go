// Package chunked implements a streaming decoder for chunked
// transfer-coding. Bytes may be written in arbitrarily small pieces;
// the decoder resumes across split points and never asks the caller
// to resend consumed input. It depends on frame for chunk framing
// and (indirectly) on hexline for size-line parsing.
package chunked

import (
	"errors"

	"ontology/frame"
)

// Default limits, applied when a Config field is <= 0.
const (
	defaultMaxSizeLine = 256
	defaultMaxChunk    = 1 << 20
	defaultMaxBody     = 16 << 20
	defaultMaxTrailers = 64
)

// Config configures the decoder limits. A zero value applies the
// documented defaults. All limits are enforced eagerly: the decoder
// rejects the byte that crosses a limit instead of buffering first.
type Config struct {
	MaxSizeLine int // max bytes of a chunk-size line (content + CR)
	MaxChunk    int // max bytes declared by a single chunk
	MaxBody     int // max total decoded body bytes
	MaxTrailers int // max trailer lines
}

// Decoder is a streaming chunked transfer-coding decoder. Each
// instance is independent; a single instance is NOT safe for
// concurrent use — one goroutine must own the Write/Close calls.
type Decoder struct {
	cfg       Config
	fr        frame.Frame
	inTrailer bool
	tline     []byte // current trailer line, including a pending CR
	trailers  int
	body      []byte
	off       int64 // absolute offset of the next byte to consume
	done      bool
	err       error // terminal error, if any
}

// New returns a Decoder with the given configuration.
func New(cfg Config) *Decoder {
	if cfg.MaxSizeLine <= 0 {
		cfg.MaxSizeLine = defaultMaxSizeLine
	}
	if cfg.MaxChunk <= 0 {
		cfg.MaxChunk = defaultMaxChunk
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = defaultMaxBody
	}
	if cfg.MaxTrailers <= 0 {
		cfg.MaxTrailers = defaultMaxTrailers
	}
	d := &Decoder{cfg: cfg}
	d.fr.MaxLine = cfg.MaxSizeLine
	d.fr.OnSize = d.onSize
	return d
}

// Write consumes a piece of the encoded stream and returns the number
// of bytes consumed. Once an error is returned the decoder is
// terminal: later writes return the same error and Body is preserved.
// After Done, writes (including surplus bytes in the completing call)
// return an error matching ErrClosed.
func (d *Decoder) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.err != nil {
		return 0, d.err
	}
	if d.done {
		return 0, &Error{Err: ErrClosed, Offset: d.off, State: StateDone}
	}
	n := 0
	for n < len(p) && !d.done {
		if d.inTrailer {
			if err := d.feedTrailer(p[n]); err != nil {
				return n, d.fail(err)
			}
			n++
			d.off++
			continue
		}
		m, err := d.fr.Feed(p[n:], d.off, d.emit)
		n += m
		d.off += int64(m)
		if err != nil {
			return n, d.fail(d.convert(err))
		}
		if d.fr.State() == frame.StateDone {
			zero := d.fr.Size() == 0
			d.fr.Reset()
			if zero {
				d.inTrailer = true
			}
		}
	}
	if d.done && n < len(p) {
		return n, &Error{Err: ErrClosed, Offset: d.off, State: StateDone}
	}
	return n, nil
}

// Close declares the end of the stream. It returns nil if the message
// completed. Otherwise it returns a terminal error matching
// ErrHalfCRLF (stream stopped between CR and LF) or ErrIncomplete,
// with State telling where the decoder stopped.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.done {
		return nil
	}
	var err error
	switch {
	case !d.inTrailer && d.fr.State() == frame.StateLF:
		err = &Error{Err: ErrHalfCRLF, Offset: d.off, State: StateCRLF}
	case d.inTrailer && len(d.tline) > 0 && d.tline[len(d.tline)-1] == '\r':
		err = &Error{Err: ErrHalfCRLF, Offset: d.off, State: StateTrailer}
	default:
		err = &Error{Err: ErrIncomplete, Offset: d.off, State: d.state()}
	}
	d.err = err
	return err
}

// Done reports whether the complete message (including the empty line
// ending the trailer section) has been consumed.
func (d *Decoder) Done() bool { return d.done }

// Body returns a copy of the decoded body so far. It is never cleared
// or mutated after an error or after Done.
func (d *Decoder) Body() []byte {
	out := make([]byte, len(d.body))
	copy(out, d.body)
	return out
}

// State returns the current decoder state.
func (d *Decoder) State() State { return d.state() }

// feedTrailer consumes one byte of the trailer section. d.off still
// points at b when this is called.
func (d *Decoder) feedTrailer(b byte) error {
	if b != '\n' {
		d.tline = append(d.tline, b)
		return nil
	}
	if len(d.tline) == 0 || d.tline[len(d.tline)-1] != '\r' {
		return &Error{Err: ErrMissingCRLF, Offset: d.off, State: StateTrailer}
	}
	content := d.tline[:len(d.tline)-1]
	d.tline = d.tline[:0]
	if len(content) == 0 {
		d.done = true
		return nil
	}
	d.trailers++
	if d.trailers > d.cfg.MaxTrailers {
		return &Error{Err: ErrTooManyTrailers, Offset: d.off, State: StateTrailer}
	}
	return nil
}

// onSize enforces the per-chunk and total body limits before any data
// byte of the chunk is consumed.
func (d *Decoder) onSize(size uint64, at int64) error {
	if size > uint64(d.cfg.MaxChunk) {
		return &Error{Err: ErrChunkTooLarge, Offset: at, State: StateSizeLine}
	}
	if size > uint64(d.cfg.MaxBody)-uint64(len(d.body)) {
		return &Error{Err: ErrBodyTooLarge, Offset: at, State: StateSizeLine}
	}
	return nil
}

func (d *Decoder) emit(b []byte) { d.body = append(d.body, b...) }

// fail records the terminal error and returns it.
func (d *Decoder) fail(err error) error {
	d.err = err
	return err
}

// convert maps lower-level errors to *Error with absolute offsets.
func (d *Decoder) convert(err error) error {
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	var fe *frame.Error
	if errors.As(err, &fe) {
		return &Error{Err: fe.Err, Offset: fe.At, State: d.state()}
	}
	return err
}

func (d *Decoder) state() State {
	switch {
	case d.err != nil:
		return StateFailed
	case d.done:
		return StateDone
	case d.inTrailer:
		return StateTrailer
	}
	switch d.fr.State() {
	case frame.StateData:
		return StateChunkData
	case frame.StateCR, frame.StateLF:
		return StateCRLF
	}
	return StateSizeLine
}
