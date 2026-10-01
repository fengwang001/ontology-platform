package ws

import "sync"

const (
	bitFin  = 0x80
	bitsRSV = 0x70
	bitMask = 0x80
)

// Decoder is a server-side incremental WebSocket frame decoder.
//
// Feed is safe for concurrent use; concurrent calls are serialized and the
// observable result is equivalent to some sequential ordering. Decoding is
// deterministic with respect to the byte stream, independent of how the stream
// is split across Feed calls: the same events, violation offset and violation
// reason are produced for every chunking (including one byte per call).
type Decoder struct {
	MaxMessage int

	mu sync.Mutex

	// state: 0 open, 1 closed (close frame delivered), 2 failed (sticky).
	state int

	// total is the absolute stream offset of buf[0]: the number of bytes
	// already consumed from the stream.
	total int64

	// buf holds bytes not yet forming a fully decodable frame.
	buf []byte

	// Fragmented-message reassembly state.
	frag     bool
	fragKind int
	msg      []byte
}

// NewDecoder creates a Decoder using MaxMessageDefault.
func NewDecoder() *Decoder { return &Decoder{MaxMessage: MaxMessageDefault} }

// Feed appends inbound bytes and returns all events that became decidable.
//
// If a protocol violation is detected, the decoder enters the sticky failure
// state: the returned error wraps one of the sentinel errors (use errors.Is)
// and carries the absolute stream offset of the earliest decidable byte. Every
// later Feed returns an error wrapping ErrFailed without changing any state.
// After a close frame has been delivered, Feed returns ErrClosed. In both
// states further input is never retained or counted.
func (d *Decoder) Feed(chunk []byte) (events []Event, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch d.state {
	case 1:
		return nil, ErrClosed
	case 2:
		return nil, ErrFailed
	}

	d.buf = append(d.buf, chunk...)

	for {
		n, ev, failErr := d.process()
		if failErr != nil {
			d.state = 2
			return events, failErr
		}
		if ev != nil {
			events = append(events, *ev)
		}
		d.buf = d.buf[n:]
		d.total += int64(n)
		if n == 0 {
			break
		}
	}
	return events, nil
}

// process decodes one frame from the front of the buffer.
//
// n is the number of leading buffer bytes consumed; ev is non-nil when a frame
// event was delivered; a non-nil error is a violation. n == 0 with no error
// means more input is required.
//
// Checks are performed at the earliest buffer offset at which each violation is
// decidable; ties at the same byte follow the order documented on the package
// sentinel errors.
func (d *Decoder) process() (n int, ev *Event, err error) {
	b := d.buf
	if len(b) < 2 {
		return 0, nil, nil
	}

	b0, b1 := b[0], b[1]
	fin := b0&bitFin != 0
	opcode := int(b0 & 0x0f)

	// Byte 0: RSV, then opcode legality.
	if b0&bitsRSV != 0 {
		return 0, nil, d.violation(ErrRSV, 0)
	}
	if !validOpcode(opcode) {
		return 0, nil, d.violation(ErrOpcode, 0)
	}

	control := opcode >= 8
	dataFrame := opcode >= 1 && opcode <= 2

	// Byte 0 (continued, opcode now known): control FIN and fragmentation.
	if control && !fin {
		return 0, nil, d.violation(ErrControlFin, 0)
	}
	if opcode == 0 && !d.frag {
		return 0, nil, d.violation(ErrContinuationOutside, 0)
	}
	if dataFrame && d.frag {
		return 0, nil, d.violation(ErrNewDataInFragment, 0)
	}

	// Byte 1: MASK must be set on every inbound frame.
	if b1&bitMask == 0 {
		return 0, nil, d.violation(ErrUnmasked, 1)
	}

	indicator := int64(b1 & 0x7f)

	// A control frame cannot use an extended-length indicator: its payload is
	// at most 125 bytes, which is decidable the moment byte 1 is seen.
	if control && indicator > 125 {
		return 0, nil, d.violation(ErrControlTooLong, 1)
	}

	payloadLen := indicator
	lenEnd := 1 // offset of the final byte of the parsed length field
	switch indicator {
	case 126:
		if len(b) < 4 {
			return 0, nil, nil
		}
		payloadLen = int64(b[2])<<8 | int64(b[3])
		if payloadLen < 126 {
			return 0, nil, d.violation(ErrLengthEncoding, 2)
		}
		lenEnd = 3
	case 127:
		if len(b) < 10 {
			return 0, nil, nil
		}
		if b[2]&0x80 != 0 {
			return 0, nil, d.violation(ErrLength64Bit, 2)
		}
		for i := 2; i < 10; i++ {
			payloadLen = payloadLen<<8 | int64(b[i])
		}
		if payloadLen <= 65535 {
			return 0, nil, d.violation(ErrLengthEncoding, 2)
		}
		lenEnd = 9
	}

	// Once the true length is known, frame-level payload violations are
	// decidable at the end of the length field, before mask key or payload.
	if !control {
		if int64(len(d.msg))+payloadLen > int64(d.MaxMessage) {
			return 0, nil, d.violation(ErrMessageTooLarge, lenEnd)
		}
	} else if opcode == 8 && payloadLen == 1 {
		return 0, nil, d.violation(ErrCloseLength, lenEnd)
	}

	headerLen := lenEnd + 1
	if len(b) < headerLen+4 {
		return 0, nil, nil
	}
	maskKey := b[headerLen : headerLen+4]
	headerLen += 4

	totalLen := int64(headerLen) + payloadLen
	if int64(len(b)) < totalLen {
		return 0, nil, nil
	}

	payload := make([]byte, payloadLen)
	for i := int64(0); i < payloadLen; i++ {
		payload[i] = b[int64(headerLen)+i] ^ maskKey[i&3]
	}

	switch opcode {
	case 8:
		if payloadLen >= 2 {
			code := int(payload[0])<<8 | int(payload[1])
			if !validCloseCode(code) {
				return 0, nil, d.violation(ErrCloseCode, headerLen)
			}
		}
		out := make([]byte, len(payload))
		copy(out, payload)
		d.state = 1
		return int(totalLen), &Event{Kind: EventClose, Data: out}, nil
	case 9, 10:
		out := make([]byte, len(payload))
		copy(out, payload)
		kind := EventPing
		if opcode == 10 {
			kind = EventPong
		}
		return int(totalLen), &Event{Kind: kind, Data: out}, nil
	default:
		d.msg = append(d.msg, payload...)

		if !fin {
			d.frag = true
			d.fragKind = opcode
			return int(totalLen), nil, nil
		}

		kind := opcode
		if opcode == 0 {
			kind = d.fragKind
			d.frag = false
			d.fragKind = 0
		}
		out := d.msg
		d.msg = nil
		if kind == 1 {
			return int(totalLen), &Event{Kind: EventText, Data: out}, nil
		}
		return int(totalLen), &Event{Kind: EventBinary, Data: out}, nil
	}
}

func (d *Decoder) violation(err error, bufOffset int) error {
	return &FrameError{Err: err, Offset: d.total + int64(bufOffset)}
}

func validOpcode(op int) bool {
	switch op {
	case 0, 1, 2, 8, 9, 10:
		return true
	}
	return false
}

func validCloseCode(code int) bool {
	switch {
	case code >= 1000 && code <= 1003:
		return true
	case code >= 1007 && code <= 1011:
		return true
	case code >= 3000 && code <= 4999:
		return true
	}
	return false
}
