// Package hdlc implements an HDLC-style bit-stuffing frame encoder and
// streaming decoder.
package hdlc

import (
	"errors"
	"io"
	"sync"
)

// DefaultMaxPayload is the default maximum payload length in bytes.
const DefaultMaxPayload = 1500

var (
	ErrEmptyPayload    = errors.New("hdlc: empty payload")
	ErrPayloadTooLarge = errors.New("hdlc: payload exceeds MaxPayload")
)

// flagBits is the HDLC flag 01111110 in transmission order (first bit leftmost).
var flagBits = [8]byte{0, 1, 1, 1, 1, 1, 1, 0}

// flagByte is the flag as a received-bit window value: the first received bit
// is the LSB, so 01111110 in arrival order is 0x7E.
const flagByte = 0x7E

// Encoder is a stateful stream encoder. Bits are emitted LSB-first within each
// output byte. The first frame is preceded by a flag; every frame is followed
// by a flag that doubles as the next frame's opening flag. It is safe for
// concurrent use; concurrent calls behave as some serial order.
type Encoder struct {
	mu         sync.Mutex
	w          io.Writer
	maxPayload int
	started    bool
	bitBuf     byte
	bitCount   int
	err        error
}

func NewEncoder(w io.Writer, maxPayload int) *Encoder {
	return &Encoder{w: w, maxPayload: maxPayload}
}

// WriteFrame encodes one frame (payload + 2-byte FCS, low byte first) with bit
// stuffing. Empty or oversized payloads are rejected without emitting any bit
// or changing the shared-flag state and bit buffer.
func (e *Encoder) WriteFrame(payload []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(payload) == 0 {
		return ErrEmptyPayload
	}
	if len(payload) > e.maxPayload {
		return ErrPayloadTooLarge
	}
	if e.err != nil {
		return e.err
	}
	if !e.started {
		e.emitBits(flagBits[:])
		e.started = true
	}
	fcs := crc16X25(payload)
	content := make([]byte, 0, len(payload)+2)
	content = append(content, payload...)
	content = append(content, byte(fcs), byte(fcs>>8))
	ones := 0
	for _, b := range content {
		for i := 0; i < 8; i++ {
			bit := (b >> i) & 1
			e.emitBit(bit)
			if bit == 1 {
				ones++
				if ones == 5 {
					e.emitBit(0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
	}
	e.emitBits(flagBits[:])
	return e.err
}

// Flush pads the output to a whole byte with 1 bits. The next frame afterwards
// emits a fresh opening flag.
func (e *Encoder) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for e.bitCount != 0 {
		e.emitBit(1)
	}
	e.started = false
	return e.err
}

func (e *Encoder) emitBits(bits []byte) {
	for _, b := range bits {
		e.emitBit(b)
	}
}

func (e *Encoder) emitBit(bit byte) {
	if e.err != nil {
		return
	}
	e.bitBuf |= bit << e.bitCount
	e.bitCount++
	if e.bitCount == 8 {
		if _, err := e.w.Write([]byte{e.bitBuf}); err != nil {
			e.err = err
		}
		e.bitBuf = 0
		e.bitCount = 0
	}
}

// Stats reports decoder counters.
type Stats struct {
	FramesDelivered  int
	Aborts           int
	DroppedBadLength int
	DroppedTooShort  int
	DroppedBadFCS    int
}

// Decoder is a bit-level streaming decoder. Input bytes are consumed LSB-first;
// results are independent of how the input is split into Write calls. It is
// safe for concurrent use; concurrent calls behave as some serial order.
//
// Rules: whenever the most recent 8 bits equal 01111110 a flag is recognized
// and none of those 8 bits is content (adjacent flags may share the middle 0).
// Seven consecutive 1 bits are an abort: the current frame is dropped and the
// flag window restarts. Bits between flags are raw content; a 0 following five
// consecutive 1s is a stuff bit and is discarded. Zero content bits between
// two flags is idle and is ignored.
type Decoder struct {
	mu        sync.Mutex
	window    byte
	windowLen int
	ones      int
	inFrame   bool
	pending   []byte
	frames    int
	aborts    int
	badLength int
	tooShort  int
	badFCS    int
}

func NewDecoder() *Decoder {
	return &Decoder{}
}

// Write consumes bytes (LSB-first bit order) and returns the frames completed
// by these bytes, in order.
func (d *Decoder) Write(p []byte) [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out [][]byte
	for _, b := range p {
		for i := 0; i < 8; i++ {
			d.feedBit((b>>i)&1, &out)
		}
	}
	return out
}

// Stats returns a snapshot of the decoder counters.
func (d *Decoder) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return Stats{
		FramesDelivered:  d.frames,
		Aborts:           d.aborts,
		DroppedBadLength: d.badLength,
		DroppedTooShort:  d.tooShort,
		DroppedBadFCS:    d.badFCS,
	}
}

func (d *Decoder) feedBit(bit byte, out *[][]byte) {
	if d.inFrame {
		d.pending = append(d.pending, bit)
	}
	d.window = d.window>>1 | bit<<7
	if d.windowLen < 8 {
		d.windowLen++
	}
	if bit == 1 {
		d.ones++
		if d.ones == 7 {
			d.aborts++
			d.pending = d.pending[:0]
			d.window = 0
			d.windowLen = 0
			d.ones = 0
			d.inFrame = false
			return
		}
	} else {
		d.ones = 0
	}
	if d.windowLen == 8 && d.window == flagByte {
		if d.inFrame {
			n := len(d.pending) - 8
			if n < 0 {
				n = 0
			}
			d.processContent(d.pending[:n], out)
		}
		d.pending = d.pending[:0]
		d.inFrame = true
	}
}

// processContent handles the raw bits found between two flags. raw must not be
// retained by the caller afterwards.
func (d *Decoder) processContent(raw []byte, out *[][]byte) {
	if len(raw) == 0 {
		return // idle between flags
	}
	bits := make([]byte, 0, len(raw))
	ones := 0
	for _, b := range raw {
		if b == 1 {
			bits = append(bits, 1)
			ones++
			continue
		}
		if ones == 5 {
			ones = 0 // stuff bit, discard
			continue
		}
		bits = append(bits, 0)
		ones = 0
	}
	if len(bits)%8 != 0 {
		d.badLength++
		return
	}
	n := len(bits) / 8
	if n < 3 {
		d.tooShort++
		return
	}
	buf := make([]byte, n)
	for i, b := range bits {
		buf[i/8] |= b << (i % 8)
	}
	payload := buf[:n-2]
	fcs := uint16(buf[n-2]) | uint16(buf[n-1])<<8
	if crc16X25(payload) != fcs {
		d.badFCS++
		return
	}
	d.frames++
	*out = append(*out, payload)
}
