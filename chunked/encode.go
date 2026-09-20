package chunked

import (
	"errors"
	"fmt"
)

// ErrShortWrite is the cause used when a sink accepts fewer bytes than the
// encoded frame without reporting an error. A partial frame corrupts the
// chunked stream, so it must be treated as a sink failure.
var ErrShortWrite = errors.New("chunked: sink wrote fewer bytes than requested")

// writeChunk encodes a single non-empty payload as one chunk frame
// ("<hex-len>\r\n<data>\r\n") and writes the whole frame to the sink in a
// single sink.Write call. Any error or short write latches the sticky
// sink failure. The payload is never modified.
func (w *Writer) writeChunk(payload []byte) error {
	frame := makeFrame(payload)

	n, err := w.sink.Write(frame)
	if err != nil {
		return w.fail(err)
	}
	if n != len(frame) {
		return w.fail(ErrShortWrite)
	}

	w.chunks++
	return nil
}

// makeFrame builds the wire bytes for one chunk. The length is lowercase
// hexadecimal with no leading zeros or "0x" prefix.
func makeFrame(payload []byte) []byte {
	frame := make([]byte, 0, len(payload)+hexLen(len(payload))+4)
	frame = fmt.Appendf(frame, "%x", len(payload))
	frame = append(frame, '\r', '\n')
	frame = append(frame, payload...)
	frame = append(frame, '\r', '\n')
	return frame
}

// hexLen returns the number of lowercase hex digits needed to encode n.
func hexLen(n int) int {
	digits := 0
	for n > 0 {
		digits++
		n >>= 4
	}
	if digits == 0 {
		digits = 1
	}
	return digits
}
