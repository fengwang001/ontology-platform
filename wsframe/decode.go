package wsframe

import (
	"math"
	"sync"
)

const maxInt = math.MaxInt

// st states.
const (
	stOpen = iota
	stClosed
	stFailed
)

type Decoder struct {
	mu sync.Mutex

	maxMessage int

	state int

	// total is the absolute number of bytes consumed from the stream.
	total int64
	// buf holds bytes appended via Feed that belong to the frame currently
	// being parsed, indexed from frameStart (in absolute stream offsets).
	buf []byte

	// Fragmented-message reassembly state.
	frag   bool
	msgOp  byte
	msg    []byte
	msgLen int
}

func NewDecoder(maxMessage int) *Decoder {
	return &Decoder{maxMessage: maxMessage}
}

func (d *Decoder) Feed(b []byte) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch d.state {
	case stClosed:
		return nil, ErrClosed
	case stFailed:
		return nil, ErrFailed
	}

	if len(b) > 0 {
		d.buf = append(d.buf, b...)
	}

	var events []Event
	for {
		ev, advanced, off, err := d.parseFrame()
		if err != nil {
			d.state = stFailed
			return events, decodeError(off, err)
		}
		if !advanced {
			break // incomplete frame: wait for more bytes
		}
		if ev != nil {
			events = append(events, *ev)
		}
		if d.state != stOpen {
			break
		}
	}
	return events, nil
}

// parseFrame parses exactly one frame out of d.buf. When advanced is false
// the frame is incomplete and no bytes were consumed; a consumed frame that
// produces no event (a non-final fragment) returns advanced=true, ev=nil.
// On a protocol violation it returns the reason and the absolute offset of
// the earliest byte at which the violation is decidable.
func (d *Decoder) parseFrame() (ev *Event, advanced bool, off int64, err error) {
	base := d.total // absolute offset of d.buf[0]
	n := len(d.buf)
	if n < 2 {
		return nil, false, 0, nil
	}

	b0 := d.buf[0]
	b1 := d.buf[1]
	fin := b0&0x80 != 0
	rsv := b0 & 0x70
	op := b0 & 0x0f
	masked := b1&0x80 != 0
	p7 := int(b1 & 0x7f)

	// Byte 0 checks, in the mandated precedence order.
	if rsv != 0 {
		return nil, false, base + 0, ErrRSV
	}
	if !legalOpcode(op) {
		return nil, false, base + 0, ErrOpcode
	}
	isControl := op >= 0x8
	if isControl && !fin {
		return nil, false, base + 0, ErrControlFragment
	}
	if !isControl {
		switch {
		case d.frag && op != OpContinuation:
			return nil, false, base + 0, ErrNewDataFragment
		case !d.frag && op == OpContinuation:
			return nil, false, base + 0, ErrUnexpectedCont
		}
	}

	// Byte 1 checks.
	if !masked {
		return nil, false, base + 1, ErrUnmasked
	}

	// Length field.
	var payloadLen int
	var maskOff, lenEndOff int64
	switch {
	case p7 < 126:
		payloadLen = p7
		lenEndOff = 2
	case p7 == 126:
		if n < 4 {
			return nil, false, 0, nil
		}
		// Non-minimal encoding is decidable once the two length bytes are
		// present; the earliest byte carrying that fact is the second one
		// (the top byte must be >= 1).
		v := int(d.buf[2])<<8 | int(d.buf[3])
		if v < 126 {
			return nil, false, base + 3, ErrLengthEncoding
		}
		payloadLen = v
		lenEndOff = 4
	default: // p7 == 127
		// High bit is decidable as soon as the first length byte arrives.
		if n < 3 {
			return nil, false, 0, nil
		}
		if d.buf[2]&0x80 != 0 {
			return nil, false, base + 2, ErrLength64Bit
		}
		// Once the top six length bytes are known to all be zero, the
		// value is at most 65535 regardless of the remaining two bytes:
		// the 64-bit form is non-minimal. That fact is decidable at the
		// sixth extension byte (absolute offset base+7).
		if n >= 8 {
			allZero := true
			for i := 2; i < 8; i++ {
				if d.buf[i] != 0 {
					allZero = false
					break
				}
			}
			if allZero {
				return nil, false, base + 7, ErrLengthEncoding
			}
		}
		if n < 10 {
			return nil, false, 0, nil
		}
		v := int64(0)
		for i := 2; i < 10; i++ {
			v = v<<8 | int64(d.buf[i])
		}
		// Defense in depth: reaching here with v <= 65535 would mean the
		// early check above missed a zero run; values <= 65535 must use a
		// shorter encoding.
		if v <= 65535 {
			return nil, false, base + 7, ErrLengthEncoding
		}
		if v > int64(maxInt) {
			if isControl {
				return nil, false, base + 9, ErrControlTooLong
			}
			return nil, false, base + 9, ErrMessageTooLarge
		}
		payloadLen = int(v)
		lenEndOff = 10
	}

	// Semantic checks decidable at the end of the length field.
	frameLen := lenEndOff + 4 + int64(payloadLen)
	if isControl && payloadLen > 125 {
		return nil, false, base + lenEndOff - 1, ErrControlTooLong
	}
	if !isControl && d.msgLen+payloadLen > d.maxMessage {
		return nil, false, base + lenEndOff - 1, ErrMessageTooLarge
	}

	maskOff = lenEndOff
	if int64(n) < frameLen {
		return nil, false, 0, nil
	}

	// Whole frame is present: unmask a copy of the payload.
	key := d.buf[maskOff : maskOff+4]
	payload := make([]byte, payloadLen)
	payStart := maskOff + 4
	for i := 0; i < payloadLen; i++ {
		payload[i] = d.buf[payStart+int64(i)] ^ key[i%4]
	}

	if op == OpClose {
		if payloadLen == 1 {
			return nil, false, base + frameLen - 1, ErrCloseLength
		}
		if payloadLen >= 2 {
			code := int(payload[0])<<8 | int(payload[1])
			if !legalCloseCode(code) {
				return nil, false, base + payStart + 1, ErrCloseCode
			}
		}
	}

	// Consume the frame.
	d.consume(frameLen)

	var out Event
	switch {
	case isControl:
		switch op {
		case OpPing:
			out = Event{Kind: KindPing, Op: op, Payload: payload}
		case OpPong:
			out = Event{Kind: KindPong, Op: op, Payload: payload}
		default:
			out = Event{Kind: KindClose, Op: op, Payload: payload}
			d.state = stClosed
		}
	case op == OpContinuation:
		d.msg = append(d.msg, payload...)
		d.msgLen += len(payload)
		if fin {
			out = Event{Kind: KindMessage, Op: d.msgOp, Payload: d.msg}
			d.frag = false
			d.msg = nil
			d.msgLen = 0
			return &out, true, 0, nil
		}
		return nil, true, 0, nil
	default: // start frame, text or binary
		if fin {
			out = Event{Kind: KindMessage, Op: op, Payload: payload}
			return &out, true, 0, nil
		} else {
			d.frag = true
			d.msgOp = op
			d.msg = payload
			d.msgLen = len(payload)
			return nil, true, 0, nil
		}
	}
	return &out, true, 0, nil
}

func (d *Decoder) consume(frameLen int64) {
	d.total += frameLen
	// frameLen is small relative to buf because the whole frame is present.
	rest := d.buf[frameLen:]
	if len(rest) == 0 {
		d.buf = d.buf[:0]
	} else {
		copy(d.buf, rest)
		d.buf = d.buf[:len(rest)]
	}
}

func legalOpcode(op byte) bool {
	switch op {
	case OpContinuation, OpText, OpBinary, OpClose, OpPing, OpPong:
		return true
	}
	return false
}

func legalCloseCode(code int) bool {
	switch {
	case 1000 <= code && code <= 1003:
		return true
	case 1007 <= code && code <= 1011:
		return true
	case 3000 <= code && code <= 4999:
		return true
	}
	return false
}
