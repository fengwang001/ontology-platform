package cobs

import (
	"bytes"
	"sync"
)

// MaxData is the largest payload accepted by Encoder.
const (
	MaxData  = 65535
	MaxFrame = MaxData + (MaxData+253)/254
)

// Encoder creates delimited canonical COBS frames and records successful output.
type Encoder struct {
	mu     sync.Mutex
	frames uint64
	bytes  uint64
}

// FrameHandler receives one successfully decoded payload.
type FrameHandler func(payload []byte)

// Decoder is a streaming, concurrency-safe COBS frame decoder.
type Decoder struct {
	mu sync.Mutex

	onFrame  FrameHandler
	buf      []byte
	frameLen int
	tooLong  bool

	frames       uint64
	empties      uint64
	tooLongCount uint64
	truncated    uint64
	nonCanonical uint64
}

// NewEncoder creates a framed COBS encoder.
func NewEncoder() *Encoder {
	return &Encoder{}
}

// NewDecoder creates a decoder that invokes onFrame for every valid frame.
func NewDecoder(onFrame FrameHandler) *Decoder {
	return &Decoder{onFrame: onFrame}
}

// Encode returns canonical COBS data followed by a zero frame delimiter.
func (e *Encoder) Encode(data []byte) ([]byte, error) {
	if len(data) > MaxData {
		return nil, ErrDataTooLong
	}

	encoded, err := encodeCanonical(data)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, len(encoded)+1)
	copy(frame, encoded)
	frame[len(encoded)] = 0

	e.mu.Lock()
	e.frames++
	e.bytes += uint64(len(frame))
	e.mu.Unlock()

	return frame, nil
}

// Frames returns the number of successfully encoded frames.
func (e *Encoder) Frames() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.frames
}

// Bytes returns the total number of bytes returned by successful encodes.
func (e *Encoder) Bytes() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bytes
}

// Write consumes a chunk of a delimited COBS byte stream.
func (d *Decoder) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, b := range p {
		if b == 0 {
			switch {
			case d.tooLong:
				d.tooLongCount++
			case d.frameLen == 0:
				d.empties++
			default:
				payload, err := decodeFrame(d.buf[:d.frameLen])
				if err != nil {
					switch err {
					case ErrTruncatedBlock:
						d.truncated++
					case ErrNonCanonical:
						d.nonCanonical++
					}
				} else {
					d.frames++
					if d.onFrame != nil {
						d.onFrame(payload)
					}
				}
			}

			d.buf = d.buf[:0]
			d.frameLen = 0
			d.tooLong = false
			continue
		}

		d.frameLen++
		if d.frameLen > MaxFrame {
			d.tooLong = true
			continue
		}
		d.buf = append(d.buf, b)
	}
	return len(p), nil
}

// Frames returns the number of valid decoded frames.
func (d *Decoder) Frames() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.frames
}

// Empties returns empty frames caused by adjacent zero delimiters.
func (d *Decoder) Empties() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.empties
}

// FrameTooLongs returns frames discarded after exceeding MaxFrame.
func (d *Decoder) FrameTooLongs() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tooLongCount
}

// Truncated returns frames whose final block lacked enough bytes.
func (d *Decoder) Truncated() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.truncated
}

// NonCanonical returns syntactically decodable but non-canonical frames.
func (d *Decoder) NonCanonical() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nonCanonical
}

// DecodeFrame validates one frame without its trailing zero delimiter.
func DecodeFrame(frame []byte) ([]byte, error) {
	if len(frame) > MaxFrame {
		return nil, ErrFrameTooLong
	}
	return decodeFrame(frame)
}

func encodeCanonical(data []byte) ([]byte, error) {
	if len(data) > MaxData {
		return nil, ErrDataTooLong
	}

	encoded := make([]byte, 0, len(data)+(len(data)+253)/254+1)
	for start := 0; start < len(data); {
		end := start
		for end < len(data) && data[end] != 0 && end-start < 254 {
			end++
		}

		blockLen := end - start
		encoded = append(encoded, byte(blockLen+1))
		encoded = append(encoded, data[start:end]...)
		start = end

		if blockLen == 254 {
			continue
		}
		if start == len(data) {
			break
		}

		start++
		if start == len(data) {
			encoded = append(encoded, 1)
			break
		}
	}
	if len(data) == 0 {
		encoded = append(encoded, 1)
	}
	return encoded, nil
}

func decodeFrame(frame []byte) ([]byte, error) {
	payload := make([]byte, 0, len(frame))

	for pos := 0; pos < len(frame); {
		code := int(frame[pos])
		blockLen := code - 1
		pos++

		if pos+blockLen > len(frame) {
			return nil, ErrTruncatedBlock
		}
		for _, b := range frame[pos : pos+blockLen] {
			if b == 0 {
				return nil, ErrNonCanonical
			}
			payload = append(payload, b)
		}
		pos += blockLen

		if code < 255 && pos < len(frame) {
			payload = append(payload, 0)
		}
	}

	canonical, err := encodeCanonical(payload)
	if err != nil || !bytes.Equal(canonical, frame) {
		return nil, ErrNonCanonical
	}
	return payload, nil
}
