package chunked

import (
	"fmt"
	"io"
)

// Writer encodes writes into HTTP/1.1-style chunked transfer coding
// blocks and forwards them to an underlying sink. It is not safe for
// concurrent use.
type Writer struct {
	sink     io.Writer
	maxChunk int // payload bytes per chunk; <=0 means unlimited
	chunks   int // data chunks written so far (excluding the last chunk)
	closed   bool
	err      error // sticky sink failure, wraps ErrSinkFail
}

// New returns a Writer that encodes into sink. maxChunk is the maximum
// payload size of a single chunk in bytes; <=0 means no limit.
func New(sink io.Writer, maxChunk int) *Writer {
	return &Writer{sink: sink, maxChunk: maxChunk}
}

// Write encodes p into one or more chunks and writes them to the sink.
// It returns the number of bytes of p consumed. A zero-length p is a
// no-op: it consumes nothing and emits no chunk, because an empty chunk
// would be indistinguishable from the end-of-stream marker.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.closed {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}

	consumed := 0
	for consumed < len(p) {
		n := len(p) - consumed
		if w.maxChunk > 0 && n > w.maxChunk {
			n = w.maxChunk
		}
		payload := p[consumed : consumed+n]
		if err := w.emit(appendChunk(nil, payload)); err != nil {
			return consumed, err
		}
		consumed += n
		w.chunks++
	}
	return consumed, nil
}

// Close writes the terminating "0\r\n\r\n" chunk. It is idempotent:
// only the first successful call emits bytes, later calls return nil.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return nil
	}
	if err := w.emit([]byte(lastChunk)); err != nil {
		return err
	}
	w.closed = true
	return nil
}

// Chunks reports how many data chunks have been written so far,
// excluding the terminating chunk.
func (w *Writer) Chunks() int {
	return w.chunks
}

// emit writes one fully encoded block to the sink. Any failure —
// including a short write without an error — is recorded as a sticky
// error wrapping ErrSinkFail, and no further sink writes are attempted.
func (w *Writer) emit(block []byte) error {
	n, err := w.sink.Write(block)
	if err == nil && n < len(block) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = fmt.Errorf("%w: %w", ErrSinkFail, err)
		return w.err
	}
	return nil
}
