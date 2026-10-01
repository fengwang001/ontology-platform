// Package modbusrtu implements a Modbus RTU frame receiver state machine
// driven by an injected clock.
package modbusrtu

import (
	"errors"
	"sync"
	"time"
)

// maxFrameLen is the maximum Modbus RTU frame length, including the two
// trailing CRC bytes. A frame is discarded the moment a 257th byte arrives.
const maxFrameLen = 256

// Errors returned by New for invalid construction parameters.
var (
	ErrInvalidBaud      = errors.New("modbusrtu: baud rate must be positive")
	ErrInvalidSlaveAddr = errors.New("modbusrtu: slave address must be in 1..247")
)

// Event describes the single outcome of one OnByte or Poll call.
type Event struct {
	Kind   EventKind
	Frame  []byte
	Time   time.Time
	Reason error
}

// EventKind enumerates the possible outcomes of one call.
type EventKind int

const (
	EventNone EventKind = iota
	EventDelivered
	EventIgnored
	EventDropped
)

// Sentinel drop reasons, matchable via errors.Is.
var (
	ErrClockBackward = errors.New("modbusrtu: clock moved backwards")
	ErrIntraGap      = errors.New("modbusrtu: intra-frame gap violation")
	ErrFrameTooLong  = errors.New("modbusrtu: frame longer than 256 bytes")
	ErrFrameTooShort = errors.New("modbusrtu: frame shorter than 4 bytes")
	ErrCRC           = errors.New("modbusrtu: CRC mismatch")
)

// Counters holds per-outcome tallies.
type Counters struct {
	Delivered uint64
	Ignored   uint64
	IntraGap  uint64
	TooLong   uint64
	TooShort  uint64
	CRCErrors uint64
}

// Receiver reassembles RTU frames from timestamped bytes.
type Receiver struct {
	mu        sync.Mutex
	baud      int
	slaveAddr byte
	t15       time.Duration
	t35       time.Duration

	// Monotonic logic-clock of injected times. Equal times are accepted;
	// any strictly earlier time is rejected without touching state.
	lastCall time.Time
	haveCall bool

	// lastByte is updated by every byte, including bytes swallowed while the
	// receiver is in its discard state. Poll never updates it.
	lastByte time.Time
	haveByte bool

	// discarding means an intra-frame gap violation or an over-long frame has
	// voided the current frame; all further bytes are ignored until a gap of
	// at least t3.5 to the previous byte is observed.
	discarding bool

	buf []byte

	counters Counters
}

// New validates and constructs a Receiver.
func New(baud int, slaveAddr byte) (*Receiver, error) {
	if baud <= 0 {
		return nil, ErrInvalidBaud
	}
	if slaveAddr < 1 || slaveAddr > 247 {
		return nil, ErrInvalidSlaveAddr
	}
	var t15ns, t35ns int64
	if baud >= 19200 {
		// Character time is fixed above 19200: t1.5 = 750us, t3.5 = 1750us.
		t15ns = 750_000
		t35ns = 1_750_000
	} else {
		// 11-bit characters; integer division floors the result.
		t15ns = (33 * 1_000_000_000) / int64(2*baud)
		t35ns = (77 * 1_000_000_000) / int64(2*baud)
	}
	return &Receiver{
		baud:      baud,
		slaveAddr: slaveAddr,
		t15:       time.Duration(t15ns),
		t35:       time.Duration(t35ns),
		buf:       make([]byte, 0, maxFrameLen+1),
	}, nil
}

// OnByte feeds one received byte observed at time t.
func (r *Receiver) OnByte(b byte, t time.Time) (*Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.haveCall && t.Before(r.lastCall) {
		return nil, ErrClockBackward
	}
	r.lastCall, r.haveCall = t, true

	var ev *Event
	gap := time.Duration(0)
	if r.haveByte {
		gap = t.Sub(r.lastByte)
	}

	if r.haveByte && gap >= r.t35 {
		// Left-closed boundary: the previous accumulation ends first, even if
		// we were discarding (in which case there is nothing to settle), and
		// this byte opens a fresh frame.
		if !r.discarding {
			ev = r.settle(t)
		}
		r.discarding = false
		r.buf = r.buf[:0]
		r.buf = append(r.buf, b)
	} else if r.discarding {
		// Swallow the byte. Only a gap >= t3.5 can leave discard mode.
	} else if r.haveByte && gap > r.t15 {
		// t15 itself is allowed; strictly greater than t15 but below t35 is an
		// intra-frame gap violation.
		r.discarding = true
		r.buf = r.buf[:0]
		r.counters.IntraGap++
		ev = &Event{Kind: EventDropped, Time: t, Reason: ErrIntraGap}
	} else {
		if len(r.buf) >= maxFrameLen {
			// The 257th byte voids the frame immediately.
			r.discarding = true
			r.buf = r.buf[:0]
			r.counters.TooLong++
			ev = &Event{Kind: EventDropped, Time: t, Reason: ErrFrameTooLong}
		} else {
			r.buf = append(r.buf, b)
		}
	}

	r.lastByte, r.haveByte = t, true
	return ev, nil
}

// Poll advances time without receiving a byte.
func (r *Receiver) Poll(t time.Time) (*Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.haveCall && t.Before(r.lastCall) {
		return nil, ErrClockBackward
	}
	r.lastCall, r.haveCall = t, true

	if !r.haveByte || t.Sub(r.lastByte) < r.t35 {
		return nil, nil
	}
	if r.discarding {
		r.discarding = false
		r.buf = r.buf[:0]
		return nil, nil
	}
	return r.settle(t), nil
}

// Snapshot returns a point-in-time copy of the counters.
func (r *Receiver) Snapshot() Counters {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters
}

// Thresholds exposes the configured gap thresholds.
func (r *Receiver) Thresholds() (t15, t35 time.Duration) {
	return r.t15, r.t35
}

// settle classifies and finalizes the current buffer, producing at most one
// event. Checks are reported in strict order: short frame, CRC, address.
// Caller must hold r.mu.
func (r *Receiver) settle(at time.Time) *Event {
	frame := append([]byte(nil), r.buf...)
	r.buf = r.buf[:0]
	// The accumulation is consumed; without this a later Poll would re-settle
	// an empty buffer as a zero-length short frame.
	r.haveByte = false

	if len(frame) < 4 {
		r.counters.TooShort++
		return &Event{Kind: EventDropped, Frame: frame, Time: at, Reason: ErrFrameTooShort}
	}

	got := uint16(frame[len(frame)-2]) | uint16(frame[len(frame)-1])<<8
	want := crc16Modbus(frame[:len(frame)-2])
	if got != want {
		r.counters.CRCErrors++
		return &Event{Kind: EventDropped, Frame: frame, Time: at, Reason: ErrCRC}
	}

	if addr := frame[0]; addr != r.slaveAddr && addr != 0 {
		r.counters.Ignored++
		return &Event{Kind: EventIgnored, Frame: frame, Time: at, Reason: nil}
	}

	r.counters.Delivered++
	return &Event{Kind: EventDelivered, Frame: frame, Time: at, Reason: nil}
}

// crc16Modbus computes CRC-16/MODBUS: init 0xFFFF, reflected polynomial
// 0xA001, no final xor. Check value over "123456789" is 0x4B37.
func crc16Modbus(p []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range p {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
