package framing

import "encoding/binary"

const headerLen = 4

type state int

const (
	stateOpen state = iota
	stateFailed
	stateClosed
)

// Reader incrementally splits a byte stream into length-prefixed frames.
type Reader struct {
	max      int
	buf      []byte
	st       state
	closeErr error
}

// New returns a Reader that rejects frames whose payload exceeds maxFrame
// bytes.
func New(maxFrame int) *Reader {
	return &Reader{max: maxFrame}
}

// Feed consumes an arbitrary chunk of the stream and returns any frames
// completed by it. Returned frames are copies, fully isolated from p and
// from the reader's internal buffer.
func (r *Reader) Feed(p []byte) ([][]byte, error) {
	switch r.st {
	case stateFailed:
		return nil, ErrFrameTooLarge
	case stateClosed:
		return nil, ErrClosed
	}

	r.buf = append(r.buf, p...)

	var frames [][]byte
	consumed := 0
	for len(r.buf)-consumed >= headerLen {
		n := int(binary.BigEndian.Uint32(r.buf[consumed : consumed+headerLen]))
		if n > r.max {
			r.buf = r.buf[consumed:]
			r.st = stateFailed
			return frames, ErrFrameTooLarge
		}
		if len(r.buf)-consumed < headerLen+n {
			break
		}
		frame := make([]byte, n)
		copy(frame, r.buf[consumed+headerLen:consumed+headerLen+n])
		frames = append(frames, frame)
		consumed += headerLen + n
	}

	r.buf = r.buf[consumed:]
	if len(r.buf) == 0 {
		r.buf = nil
	}
	return frames, nil
}

// Close signals end of stream.
// It reports ErrIncomplete if a partial frame remains buffered, and is
// idempotent: repeated calls return the same result.
func (r *Reader) Close() error {
	switch r.st {
	case stateFailed:
		return ErrFrameTooLarge
	case stateClosed:
		return r.closeErr
	}
	r.st = stateClosed
	if len(r.buf) > 0 {
		r.closeErr = ErrIncomplete
	}

	return r.closeErr
}

// Buffered reports how many not-yet-framed bytes are held internally.
func (r *Reader) Buffered() int {
	return len(r.buf)
}
