package chunked

import "io"

// Writer encodes data written to it into the chunked transfer encoding and
// forwards the encoded bytes to an underlying sink.
type Writer struct {
	sink     io.Writer
	maxChunk int

	chunks  int
	closed  bool
	sinkErr error
}

// New returns a chunked Writer wrapping w. A positive maxChunk caps the
// payload size of a single chunk; maxChunk <= 0 means unlimited.
func New(w io.Writer, maxChunk int) *Writer {
	return &Writer{sink: w, maxChunk: maxChunk}
}

// Write encodes p as one or more chunks and writes them to the sink.
//
// A zero-length p produces no chunk and returns (0, nil), because a
// zero-length chunk is the stream terminator. When maxChunk is positive, p
// is split into payloads of at most maxChunk bytes without copying or
// modifying p. Write returns the number of payload bytes consumed; on a
// sink failure this is the number encoded before the failure.
func (w *Writer) Write(p []byte) (int, error) {
	if w.sinkErr != nil {
		return 0, w.sinkErr
	}
	if w.closed {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}

	consumed := 0
	for len(p) > 0 {
		payload := p
		if w.maxChunk > 0 && len(payload) > w.maxChunk {
			payload = payload[:w.maxChunk]
		}
		if err := w.writeChunk(payload); err != nil {
			return consumed, err
		}
		consumed += len(payload)
		p = p[len(payload):]
	}
	return consumed, nil
}

// terminalChunk is the zero-length last-chunk marker that ends the stream.
var terminalChunk = []byte("0\r\n\r\n")

// Close writes the terminating "0\r\n\r\n" chunk exactly once. Repeated
// calls after a successful close return nil without touching the sink.
// After a sink failure, Close keeps returning the sticky ErrSinkFail error.
func (w *Writer) Close() error {
	if w.sinkErr != nil {
		return w.sinkErr
	}
	if w.closed {
		return nil
	}

	n, err := w.sink.Write(terminalChunk)
	if err != nil {
		return w.fail(err)
	}
	if n != len(terminalChunk) {
		return w.fail(ErrShortWrite)
	}

	w.closed = true
	return nil
}

// Chunks reports the number of non-terminal chunks written so far.
func (w *Writer) Chunks() int {
	return w.chunks
}
