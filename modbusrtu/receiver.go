// Package modbusrtu implements a Modbus RTU frame receiver driven by an
// injected clock. Frame boundaries are derived from inter-byte silence
// intervals rather than an external timer, which makes frame loss reasons
// exactly reproducible.
package modbusrtu

import (
	"errors"
	"sync"
	"time"
)

// Frame loss reasons. All of them are detectable with errors.Is.
var (
	// ErrIntraFrameGap is reported when bytes of one frame are separated by
	// more than 1.5 character times but less than 3.5 character times.
	ErrIntraFrameGap = errors.New("modbusrtu: intra-frame gap violation")
	// ErrFrameTooLong is reported on the 257th byte of a frame.
	ErrFrameTooLong = errors.New("modbusrtu: frame longer than 256 bytes")
	// ErrFrameTooShort is reported at settlement when a frame has fewer than
	// 4 bytes.
	ErrFrameTooShort = errors.New("modbusrtu: frame shorter than 4 bytes")
	// ErrCRC is reported when the CRC-16/MODBUS check does not match.
	ErrCRC = errors.New("modbusrtu: crc mismatch")
	// ErrClockBackward is reported when a call carries a timestamp earlier
	// than the timestamp of the previous call.
	ErrClockBackward = errors.New("modbusrtu: clock moved backwards")
)

// maxFramePayload is the maximum number of bytes allowed in one RTU frame.
const maxFramePayload = 256

// Event is the single outcome a call may produce. At most one Event is
// returned by each OnByte or Poll call.
type Event struct {
	// Frame holds the complete frame, including address, function, data and
	// the two CRC bytes. It is nil for loss events.
	Frame []byte
	// Reason is nil for a delivered frame, otherwise one of the frame loss
	// sentinel errors.
	Reason error
	// Delivered reports whether the frame was handed to the caller.
	Delivered bool
	// Ignored reports whether an otherwise valid frame was addressed to
	// neither this station nor the broadcast address 0.
	Ignored bool
}

// Counters summarizes receiver history.
type Counters struct {
	Delivered int
	Ignored   int
	IntraGap  int
	TooLong   int
	TooShort  int
	CRCErrors int
}

// Receiver is a concurrency-safe Modbus RTU frame receiver.
type Receiver struct {
	mu sync.Mutex

	baud int
	addr int

	// t15 and t35 are the 1.5 and 3.5 character-time gaps in nanoseconds.
	t15 time.Duration
	t35 time.Duration

	// started is false until the first accepted call.
	started bool
	// lastCall is the timestamp of the previous accepted call.
	lastCall time.Time

	// inFrame reports whether bytes belong to a frame currently being
	// assembled.
	inFrame bool
	// discarding marks the intra-frame-gap / over-long discard state.
	discarding bool
	// lastByte is the timestamp of the previous byte, including bytes that
	// were ignored while discarding.
	lastByte time.Time

	buf []byte

	counters Counters
}

// New returns a Receiver for baud rate baud and local slave address addr.
// A Modbus character takes 11 bit times. At baud rates >= 19200 the gaps are
// fixed at 750us (t1.5) and 1750us (t3.5); below that they are derived from
// floor(33e9/(2*baud)) and floor(77e9/(2*baud)) nanoseconds.
func New(baud int, addr int) (*Receiver, error) {
	if baud <= 0 {
		return nil, errors.New("modbusrtu: baud rate must be positive")
	}
	if addr < 1 || addr > 247 {
		return nil, errors.New("modbusrtu: slave address must be in 1..247")
	}
	r := &Receiver{baud: baud, addr: addr}
	if baud >= 19200 {
		r.t15 = 750 * time.Microsecond
		r.t35 = 1750 * time.Microsecond
	} else {
		r.t15 = time.Duration(33_000_000_000/int64(2*baud)) * time.Nanosecond
		r.t35 = time.Duration(77_000_000_000/int64(2*baud)) * time.Nanosecond
	}
	r.buf = make([]byte, 0, maxFramePayload+1)
	return r, nil
}

// OnByte feeds one received byte observed at time t.
func (r *Receiver) OnByte(b byte, t time.Time) (*Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started && t.Before(r.lastCall) {
		return nil, ErrClockBackward
	}
	r.started = true
	r.lastCall = t

	if r.inFrame {
		gap := t.Sub(r.lastByte)
		if gap >= r.t35 {
			if r.discarding {
				// The voided frame was already reported as lost; silence
				// releases the discard state and this byte seeds a new
				// frame without producing another event.
				r.startFrame(b, t)
				return nil, nil
			}
			// Previous frame ends at this boundary; settle it first, then
			// open a new frame with this byte.
			ev := r.settleLocked()
			r.startFrame(b, t)
			return ev, nil
		}
		if r.discarding {
			// Every byte is ignored while discarding, but gaps are still
			// measured from it.
			r.lastByte = t
			return nil, nil
		}
		if gap > r.t15 {
			// t15 < gap < t35: intra-frame gap violation. The current frame
			// is voided, this byte is ignored and the receiver stays in the
			// discard state until a gap >= t35 appears.
			r.discarding = true
			r.inFrame = true
			r.lastByte = t
			r.counters.IntraGap++
			return lossEvent(ErrIntraFrameGap), nil
		}
	}

	r.lastByte = t

	if !r.inFrame {
		r.startFrame(b, t)
		return nil, nil
	}

	if len(r.buf) >= maxFramePayload {
		// The 257th byte voids the frame immediately.
		r.discarding = true
		r.counters.TooLong++
		return lossEvent(ErrFrameTooLong), nil
	}

	r.buf = append(r.buf, b)
	return nil, nil
}

// Poll advances time without receiving a byte and may settle the current
// frame.
func (r *Receiver) Poll(t time.Time) (*Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started && t.Before(r.lastCall) {
		return nil, ErrClockBackward
	}
	r.started = true
	r.lastCall = t

	if !r.inFrame {
		return nil, nil
	}
	if t.Sub(r.lastByte) < r.t35 {
		return nil, nil
	}

	if r.discarding {
		// Silence releases the discard state without producing an event.
		r.inFrame = false
		r.discarding = false
		r.buf = r.buf[:0]
		return nil, nil
	}

	ev := r.settleLocked()
	return ev, nil
}

// Counters returns a snapshot of the frame outcome counters.
func (r *Receiver) Counters() Counters {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters
}

// startFrame opens a new frame seeded with its first byte.
func (r *Receiver) startFrame(b byte, t time.Time) {
	r.inFrame = true
	r.discarding = false
	r.buf = r.buf[:0]
	r.buf = append(r.buf, b)
	r.lastByte = t
}

// settleLocked closes and evaluates the current frame. Settlement reports at
// most the first applicable reason in this order: shorter than 4 bytes, CRC
// mismatch, address foreign to this station and to broadcast.
func (r *Receiver) settleLocked() *Event {
	frame := r.buf
	r.inFrame = false
	r.discarding = false
	r.buf = r.buf[:0]

	if len(frame) < 4 {
		r.counters.TooShort++
		return lossEvent(ErrFrameTooShort)
	}

	body := frame[:len(frame)-2]
	got := uint16(frame[len(frame)-2]) | uint16(frame[len(frame)-1])<<8
	if crc16(body) != got {
		r.counters.CRCErrors++
		return lossEvent(ErrCRC)
	}

	ev := &Event{Frame: append([]byte(nil), frame...)}
	if frame[0] != byte(r.addr) && frame[0] != 0 {
		ev.Ignored = true
		r.counters.Ignored++
		return ev
	}

	ev.Delivered = true
	r.counters.Delivered++
	return ev
}

func lossEvent(reason error) *Event {
	return &Event{Reason: reason}
}
