// Package dec implements the streaming LZ77 decompressor with strict,
// byte-offset-attributed validation. A Reader is not safe for concurrent use.
package dec

import (
	"errors"
	"fmt"

	"ontology/window"
	"ontology/wire"
)

// Distinguishable corruption errors.
var (
	ErrHeader       = errors.New("dec: bad magic or version")
	ErrZeroDist     = errors.New("dec: match distance is zero")
	ErrDistTooSoon  = errors.New("dec: distance exceeds bytes produced")
	ErrDistWindow   = errors.New("dec: distance exceeds window capacity")
	ErrLengthMismatch = errors.New("dec: end record length mismatch")
	ErrChecksum     = errors.New("dec: end record checksum mismatch")
	ErrTrailing     = errors.New("dec: trailing bytes after end record")
	ErrTruncated    = errors.New("dec: truncated stream")
	ErrLimit        = errors.New("dec: output limit exceeded")
	ErrState        = errors.New("dec: decoder in terminal error state")
)

// StreamError wraps a sentinel with the byte offset in the compressed stream.
type StreamError struct {
	Offset int
	Err    error
}

func (e *StreamError) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Offset) }
func (e *StreamError) Unwrap() error { return e.Err }

// Reader incrementally parses compressed bytes. Output is collected and
// returned by Output; Write splits must not change the result.
type Reader struct {
	buf     []byte
	out     []byte
	win     *window.Window
	sum     uint64
	decl    uint64
	limit   uint64
	base    int  // stream offset of buf[0]
	pos     int  // parse cursor inside buf
	hdr     bool
	done    bool
	sticky  error
}

// New creates a decompressor; maxOutput 0 means unlimited.
func New(maxOutput uint64) *Reader {
	return &Reader{sum: wire.FNVInitial(), limit: maxOutput}
}

func (r *Reader) fail(off int, err error) error {
	if r.sticky == nil {
		r.sticky = &StreamError{Offset: off, Err: err}
	}
	return r.sticky
}

// Write feeds more compressed bytes and parses everything currently complete.
func (r *Reader) Write(p []byte) (int, error) {
	if r.sticky != nil {
		return 0, r.sticky
	}
	r.buf = append(r.buf, p...)
	return len(p), r.parse()
}

func (r *Reader) parse() error {
	if !r.hdr {
		if err := r.parseHeader(); err != nil {
			return err
		}
	}
	for {
		off := r.base + r.pos
		tag, v, n, err := wire.ReadTag(r.buf[r.pos:])
		if err != nil {
			return r.fail(off, err)
		}
		if n == 0 {
			return nil
		}
		r.pos += n
		switch tag {
		case wire.TagLit:
			if err := r.readLit(off, v); err != nil {
				return err
			}
		case wire.TagMatch:
			if err := r.readMatch(off, v+1); err != nil {
				return err
			}
		case wire.TagFlush:
		case wire.TagDict:
			if err := r.readDict(off, v); err != nil {
				return err
			}
		case wire.TagEnd:
			if err := r.readEnd(off, v); err != nil {
				return err
			}
			if r.pos < len(r.buf) {
				return r.fail(off, ErrTrailing)
			}
			return nil
		}
	}
}

func (r *Reader) parseHeader() error {
	if len(r.buf) < wire.HeaderN {
		return nil
	}
	if r.buf[0] != wire.Magic0 || r.buf[1] != wire.Magic1 ||
		r.buf[2] != wire.Magic2 || r.buf[3] != wire.Version {
		return r.fail(0, ErrHeader)
	}
	winS, n1, e1 := wire.ReadUvarint(r.buf[4:])
	if e1 != nil {
		return r.fail(4, e1)
	}
	if n1 == 0 || len(r.buf) < 4+n1+1 {
		return nil
	}
	chain, n2, e2 := wire.ReadUvarint(r.buf[4+n1:])
	if e2 != nil {
		return r.fail(4+n1, e2)
	}
	if n2 == 0 {
		return nil
	}
	win, err := window.New(int(winS))
	if err != nil || chain == 0 || chain > 1<<20 {
		return r.fail(4, ErrHeader)
	}
	r.win = win
	r.pos = 4 + n1 + n2
	r.hdr = true
	return nil
}

func (r *Reader) varint(off int) (uint64, bool, error) {
	v, n, err := wire.ReadUvarint(r.buf[r.pos:])
	if err != nil {
		return 0, false, r.fail(r.base+r.pos, err)
	}
	if n == 0 {
		return 0, false, nil
	}
	r.pos += n
	return v, true, nil
}

func (r *Reader) readLit(off int, lenM1 uint64) error {
	if err != nil || !ok {
		return err
	}
	n := int(lenM1) + 1
	if uint64(len(r.buf)-r.pos) < uint64(n) {
		return nil
	}
	b := r.buf[r.pos : r.pos+n]
	if !r.room(uint64(n)) {
		return r.fail(off, ErrLimit)
	}
	r.emit(b)
	r.pos += n
	return nil
}

func (r *Reader) readMatch(off, distM1 uint64) error {
	d := int(distM1) + 1
	lm, ok, err := r.varint(off)
	if err != nil || !ok {
		return err
	}
	n := int(lm) + wire.MinMatch
	if d == 0 {
		return r.fail(off, ErrZeroDist)
	}
	if uint64(d) > uint64(len(r.out)) {
		return r.fail(off, ErrDistTooSoon)
	}
	if d > r.win.Cap() {
		return r.fail(off, ErrDistWindow)
	}
	if !r.room(uint64(n)) {
		return r.fail(off, ErrLimit)
	}
	for i := 0; i < n; i++ { // byte-wise: distance may be < length
		r.out = r.win.Copy(r.out, d, 1)
		r.sum = wire.FNV(r.sum, r.out[len(r.out)-1:])
	}
	return nil
}

func (r *Reader) readDict(off, n uint64) error {
	if int(n) > len(r.out) {
		return r.fail(off, ErrDistTooSoon)
	}
	if int(n) > r.win.Cap() {
		return r.fail(off, ErrDistWindow)
	}
	// Dict bytes are the tail of already produced output; they are already in
	// the window, so nothing more is emitted.
	return nil
}

func (r *Reader) readEnd(off, total uint64) error {
	s, ok, err := r.varint(off)
	if err != nil || !ok {
		return err
	}
	if total != uint64(len(r.out)) {
		return r.fail(off, ErrLengthMismatch)
	}
	if s != r.sum {
		return r.fail(off, ErrChecksum)
	}
	r.decl, r.done = total, true
	r.compact()
	return nil
}

func (r *Reader) room(n uint64) bool {
	return r.limit == 0 || uint64(len(r.out))+n <= r.limit
}

func (r *Reader) emit(b []byte) {
	r.out = append(r.out, b...)
	r.win.Append(b)
	r.sum = wire.FNV(r.sum, b)
}

func (r *Reader) compact() {
	if r.pos == 0 {
		return
	}
	r.buf = append([]byte(nil), r.buf[r.pos:]...)
	r.base += r.pos
	r.pos = 0
}

// Close reports truncation when the stream ended without a valid end record.
func (r *Reader) Close() error {
	if r.sticky != nil {
		return r.sticky
	}
	if !r.done {
		return r.fail(r.base+r.pos, ErrTruncated)
	}
	return nil
}

// Output returns the decoded bytes produced so far (a copy).
func (r *Reader) Output() []byte { return append([]byte(nil), r.out...) }
