package uart

import (
	"errors"
	"sync"
)

// Parity selects the parity mode of the UART frame.
type Parity int

const (
	ParityNone Parity = iota
	ParityEven
	ParityOdd
)

// Frame is one completed UART reception. EndIndex is the sample index at
// which the stop bit was sampled.
type Frame struct {
	Data        byte
	ParityError bool
	FrameError  bool
	Break       bool
	EndIndex    uint64
}

// Distinguishable rejection reasons.
var (
	ErrInvalidParity = errors.New("uart: invalid parity mode")
	ErrInvalidDepth  = errors.New("uart: receive queue depth must be >= 1")
	ErrInvalidLevel  = errors.New("uart: level must be 0 or 1")
	ErrQueueEmpty    = errors.New("uart: receive queue is empty")
)

type state int

const (
	stIdle       state = iota
	stStartCheck       // candidate start seen at s; deciding at s+7
	stRecv             // sampling data / parity / stop at target indices
	stWaitHigh         // stop bit was low; a high sample is required
)

// Receiver is a 16x oversampled UART receiver.
//
// Sample indices start at 0 for the first successfully fed sample and count
// up by one per sample. Any low sample while idle records a start candidate
// index s; at index s+7 a high level means glitch (back to idle), a low
// level confirms the start. Bit i is sampled at s+7+16*(i+1): data bits use
// i = 0..7, parity i = 9 (if present) and the stop bit uses i = 9 without
// parity or i = 10 with parity.
type Receiver struct {
	mu sync.Mutex

	parity Parity
	depth  int

	queue []Frame

	state  state
	index  uint64 // index of the next sample to be fed
	startS uint64 // candidate start index s
	target uint64 // next index at which a bit is sampled

	data   byte
	bitPos int // 0..7 while collecting data; 8 at parity/stop
	ones   int // count of 1 bits among data and the parity bit
	pbit   int // sampled parity bit (meaningful only with parity)

	glitches uint64
	overruns uint64
	produced uint64
}

// New constructs a Receiver with the given parity mode and queue depth D >= 1.
func New(p Parity, depth int) (*Receiver, error) {
	if p != ParityNone && p != ParityEven && p != ParityOdd {
		return nil, ErrInvalidParity
	}
	if depth < 1 {
		return nil, ErrInvalidDepth
	}
	return &Receiver{parity: p, depth: depth, queue: make([]Frame, 0, depth)}, nil
}

// Feed supplies one line sample; the sample index advances only on success.
func (r *Receiver) Feed(level int) error {
	if level != 0 && level != 1 {
		return ErrInvalidLevel
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.feedLocked(level)
	return nil
}

// FeedAll feeds a whole batch atomically. Every level is validated first; an
// invalid level rejects the whole batch and no sample is consumed, so state
// and the sample index are unchanged.
func (r *Receiver) FeedAll(levels []int) error {
	for _, level := range levels {
		if level != 0 && level != 1 {
			return ErrInvalidLevel
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, level := range levels {
		r.feedLocked(level)
	}
	return nil
}

// feedLocked processes one validated sample. The caller must hold r.mu.
func (r *Receiver) feedLocked(level int) {
	n := r.index
	r.index++

	switch r.state {
	case stIdle:
		if level == 0 {
			r.startS = n
			r.target = n + 7
			r.state = stStartCheck
		}

	case stStartCheck:
		// Samples s+1..s+6 are ignored; only s+7 is inspected.
		if n != r.target {
			break
		}
		if level == 1 {
			// Glitch: this high sample is not a new start candidate.
			r.glitches++
			r.state = stIdle
			break
		}
		r.data = 0
		r.bitPos = 0
		r.ones = 0
		r.target = r.startS + 7 + 16 // data bit 0 midpoint
		r.state = stRecv

	case stRecv:
		if n != r.target {
			break
		}
		if r.bitPos < 8 {
			if level == 1 {
				r.data |= 1 << uint(r.bitPos)
				r.ones++
			}
			r.bitPos++
			if r.bitPos < 8 {
				r.target += 16
			} else {
				// Data complete: parity bit at s+7+16*9 if present,
				// otherwise the stop bit is at the same index.
				r.bitPos = 8
				r.target = r.startS + 7 + 16*9
				if r.parity == ParityNone {
					r.bitPos = 9
				}
			}
			break
		}
		if r.bitPos == 8 {
			// Parity bit at s+7+16*9.
			r.pbit = level
			r.ones += level
			r.bitPos = 9
			r.target = r.startS + 7 + 16*10
			break
		}
		// bitPos == 9: this sample is the stop bit.
		r.complete(level, n)

	case stWaitHigh:
		// A high sample returns to idle without being a start candidate.
		if level == 1 {
			r.state = stIdle
		}
	}
}

// complete finalizes a frame sampled at stop index endIndex and enqueues it
// or counts an overrun. The caller must hold r.mu.
func (r *Receiver) complete(stopLevel int, endIndex uint64) {
	frame := Frame{Data: r.data, EndIndex: endIndex}

	switch r.parity {
	case ParityEven:
		frame.ParityError = r.ones%2 != 0
	case ParityOdd:
		frame.ParityError = r.ones%2 == 0
	}

	if stopLevel == 0 {
		frame.FrameError = true
		allZero := r.data == 0 && (r.parity == ParityNone || r.pbit == 0)
		if allZero {
			frame.Break = true
		}
	}

	r.produced++
	if len(r.queue) >= r.depth {
		r.overruns++ // existing frames are left untouched
	} else {
		r.queue = append(r.queue, frame)
	}

	if stopLevel == 0 {
		r.state = stWaitHigh
	} else {
		r.state = stIdle
	}
}

// Pop removes and returns the oldest completed frame.
func (r *Receiver) Pop() (Frame, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) == 0 {
		return Frame{}, ErrQueueEmpty
	}
	frame := r.queue[0]
	r.queue = r.queue[1:]
	return frame, nil
}

// Len returns the number of frames currently queued.
func (r *Receiver) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

// SampleIndex returns the number of successfully fed samples.
func (r *Receiver) SampleIndex() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.index
}

// GlitchCount returns the number of rejected start candidates (high at s+7).
func (r *Receiver) GlitchCount() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.glitches
}

// OverrunCount returns the number of frames dropped because the queue was full.
func (r *Receiver) OverrunCount() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.overruns
}

// ProducedCount returns the total number of produced frames, which always
// equals enqueued frames plus overruns.
func (r *Receiver) ProducedCount() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.produced
}
