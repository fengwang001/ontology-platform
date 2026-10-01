// Package cobs implements a streaming COBS (Consistent Overhead Byte
// Stuffing) framer with a canonical encoding form, frame-level rejection
// reasons, and chunk-boundary-independent decoding.
package cobs

import (
	"bytes"
	"errors"
	"sync"
)

// Frame-level rejection reasons, distinguishable via errors.Is.
var (
	// ErrDataTooLarge is returned by Encoder.Encode when len(data) > MaxData.
	ErrDataTooLarge = errors.New("cobs: data exceeds MaxData")
	// ErrFrameTooLong counts frames whose encoded length exceeds MaxFrame.
	ErrFrameTooLong = errors.New("cobs: frame exceeds MaxFrame")
	// ErrTruncated counts frames that end in the middle of a block.
	ErrTruncated = errors.New("cobs: frame ends mid-block")
	// ErrNonCanonical counts frames whose re-encoding differs from the bytes received.
	ErrNonCanonical = errors.New("cobs: frame is not in canonical form")
)

// Delimiter terminates a frame on the wire.
const Delimiter = 0x00

// maxBlockData is the maximum number of non-zero bytes in one block.
const maxBlockData = 254

// Encoder encodes payloads into canonical COBS frames. It is safe for
// concurrent use; results equal some serial order of the calls.
type Encoder struct {
	MaxData int

	mu       sync.Mutex
	frames   uint64
	bytesIn  uint64
	bytesOut uint64
}

// EncoderStats reports Encoder counters.
type EncoderStats struct {
	Frames   uint64
	BytesIn  uint64
	BytesOut uint64
}

// Encode encodes data as one canonical frame followed by Delimiter.
// If MaxData > 0 and len(data) > MaxData it returns ErrDataTooLarge and
// produces no output and no counter change. MaxData <= 0 means no limit.
func (e *Encoder) Encode(data []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.MaxData > 0 && len(data) > e.MaxData {
		return nil, ErrDataTooLarge
	}
	payload := EncodePayload(data)
	out := make([]byte, 0, len(payload)+1)
	out = append(out, payload...)
	out = append(out, Delimiter)
	e.frames++
	e.bytesIn += uint64(len(data))
	e.bytesOut += uint64(len(out))
	return out, nil
}

// EncodePayload returns the canonical COBS encoding of data, without the
// trailing Delimiter. Canonical rules: take a run of non-zero bytes, at
// most 254; a full run of 254 is emitted as code 0xFF plus the run and
// consumes no zero; a shorter run is emitted as code len+1 plus the run,
// and if data continues it consumes exactly one following zero. If that
// zero is the last byte of data, a final empty block (code 0x01) is
// appended. Empty data encodes as a single 0x01.
func EncodePayload(data []byte) []byte {
	out := make([]byte, 0, len(data)+len(data)/maxBlockData+1)
	for {
		n := 0
		for n < len(data) && n < maxBlockData && data[n] != 0 {
			n++
		}
		if n == maxBlockData {
			out = append(out, 0xFF)
			out = append(out, data[:n]...)
			data = data[n:]
			if len(data) == 0 {
				return out
			}
			continue
		}
		out = append(out, byte(n+1))
		out = append(out, data[:n]...)
		data = data[n:]
		if len(data) == 0 {
			return out
		}
		// Consume the single zero that terminated the run.
		data = data[1:]
		if len(data) == 0 {
			// The zero was the last byte: emit a final empty block.
			out = append(out, 0x01)
			return out
		}
	}
}

// Stats returns a snapshot of the encoder counters.
func (e *Encoder) Stats() EncoderStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EncoderStats{Frames: e.frames, BytesIn: e.bytesIn, BytesOut: e.bytesOut}
}

// DecoderStats reports Decoder counters.
type DecoderStats struct {
	Frames       uint64 // frames decoded and delivered
	Empties      uint64 // empty frames (adjacent delimiters), ignored
	TooLong      uint64 // frames rejected with ErrFrameTooLong
	Truncated    uint64 // frames rejected with ErrTruncated
	NonCanonical uint64 // frames rejected with ErrNonCanonical
}

// Decoder is a streaming COBS frame decoder. Bytes may be fed in any
// chunking; the delivered frame sequence and counters are identical for
// every chunking of the same byte stream. It is safe for concurrent use.
// MaxFrame <= 0 means no frame length limit.
type Decoder struct {
	MaxFrame int

	mu      sync.Mutex
	buf     []byte
	discard bool
	stats   DecoderStats
}

// Write feeds p into the decoder and returns the frames completed by
// these bytes, in order. Frame errors are counted in Stats and never
// fail the Write; decoding resynchronizes at the next Delimiter.
func (d *Decoder) Write(p []byte) [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	var frames [][]byte
	for _, b := range p {
		if b == Delimiter {
			if d.discard {
				// End of an over-long frame: resynchronize.
				d.discard = false
				d.buf = d.buf[:0]
				continue
			}
			if len(d.buf) == 0 {
				d.stats.Empties++
				continue
			}
			data, err := decodeFrame(d.buf)
			d.buf = d.buf[:0]
			switch {
			case errors.Is(err, ErrTruncated):
				d.stats.Truncated++
			case errors.Is(err, ErrNonCanonical):
				d.stats.NonCanonical++
			default:
				d.stats.Frames++
				frames = append(frames, data)
			}
			continue
		}
		if d.discard {
			continue
		}
		d.buf = append(d.buf, b)
		if d.MaxFrame > 0 && len(d.buf) > d.MaxFrame {
			// First rejection reason wins: drop the rest of the
			// frame and resynchronize at the next Delimiter.
			d.stats.TooLong++
			d.discard = true
			d.buf = d.buf[:0]
		}
	}
	return frames
}

// decodeFrame decodes one frame's encoded bytes (without Delimiter) and
// verifies canonical form. Each code byte c heads a block of c-1 bytes;
// a code below 0xFF with bytes still following implies one zero after
// the block. The decoded data re-encoded canonically must equal the
// received frame byte for byte.
func decodeFrame(frame []byte) ([]byte, error) {
	var out []byte
	i := 0
	for i < len(frame) {
		code := int(frame[i])
		i++
		n := code - 1
		if i+n > len(frame) {
			return nil, ErrTruncated
		}
		out = append(out, frame[i:i+n]...)
		i += n
		if code < 0xFF && i < len(frame) {
			out = append(out, 0)
		}
	}
	if !bytes.Equal(EncodePayload(out), frame) {
		return nil, ErrNonCanonical
	}
	return out, nil
}

// Stats returns a snapshot of the decoder counters.
func (d *Decoder) Stats() DecoderStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}
