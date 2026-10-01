package uart

import (
	"errors"
	"math/bits"
	"sync"
)

type ParityMode int

const (
	ParityNone ParityMode = iota
	ParityEven
	ParityOdd
)

type Frame struct {
	Byte           byte
	ParityError    bool
	FramingError   bool
	Break          bool
	EndSampleIndex uint64
}

var (
	ErrInvalidParity     = errors.New("uart: invalid parity mode")
	ErrInvalidQueueDepth = errors.New("uart: queue depth must be at least 1")
	ErrInvalidLevel      = errors.New("uart: level must be 0 or 1")
	ErrQueueEmpty        = errors.New("uart: receive queue is empty")
)

type receiverState int

const (
	stateIdle receiverState = iota
	stateReceiving
	stateWaitHigh
)

type Receiver struct {
	mu        sync.RWMutex
	state     receiverState
	start     uint64
	data      byte
	parityBit int
	parity    ParityMode
	queue     []Frame
	head      int
	count     int
	samples   uint64
	glitches  uint64
	overflows uint64
	produced  uint64
}

func NewReceiver(parity ParityMode, depth int) (*Receiver, error) {
	if parity < ParityNone || parity > ParityOdd {
		return nil, ErrInvalidParity
	}
	if depth < 1 {
		return nil, ErrInvalidQueueDepth
	}

	return &Receiver{
		parity: parity,
		queue:  make([]Frame, depth),
	}, nil
}

func (r *Receiver) Feed(level int) error {
	if level != 0 && level != 1 {
		return ErrInvalidLevel
	}

	r.lock()
	defer r.unlock()

	index := r.samples
	r.samples++
	r.feedLocked(level, index)
	return nil
}

func (r *Receiver) FeedAll(levels []int) error {
	for _, level := range levels {
		if level != 0 && level != 1 {
			return ErrInvalidLevel
		}
	}

	r.lock()
	defer r.unlock()

	for _, level := range levels {
		index := r.samples
		r.samples++
		r.feedLocked(level, index)
	}
	return nil
}

func (r *Receiver) Pop() (Frame, error) {
	r.lock()
	defer r.unlock()

	if r.count == 0 {
		return Frame{}, ErrQueueEmpty
	}

	frame := r.queue[r.head]
	r.queue[r.head] = Frame{}
	r.head = (r.head + 1) % len(r.queue)
	r.count--
	return frame, nil
}

func (r *Receiver) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

func (r *Receiver) SampleCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.samples
}

func (r *Receiver) GlitchCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.glitches
}

func (r *Receiver) OverflowCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.overflows
}

func (r *Receiver) ProducedCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.produced
}

func (r *Receiver) EnqueuedCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.produced - r.overflows
}

func (r *Receiver) Snapshot() []Frame {
	r.mu.RLock()
	defer r.mu.RUnlock()

	frames := make([]Frame, r.count)
	for i := range frames {
		frames[i] = r.queue[(r.head+i)%len(r.queue)]
	}
	return frames
}

func (r *Receiver) feedLocked(level int, index uint64) {
	switch r.state {
	case stateIdle:
		if level == 0 {
			r.start = index
			r.data = 0
			r.parityBit = 0
			r.state = stateReceiving
		}
	case stateReceiving:
		r.receiveLocked(level, index)
	case stateWaitHigh:
		if level == 1 {
			r.state = stateIdle
		}
	}
}

func (r *Receiver) receiveLocked(level int, index uint64) {
	offset := index - r.start
	if offset < 7 {
		return
	}

	if offset == 7 {
		if level == 1 {
			r.glitches++
			r.state = stateIdle
		}
		return
	}

	if (offset-7)%16 != 0 {
		return
	}

	bitNumber := int((offset - 7) / 16)
	switch {
	case bitNumber >= 1 && bitNumber <= 8:
		dataBit := bitNumber - 1
		if level == 1 {
			r.data |= 1 << dataBit
		}
	case bitNumber == 9 && r.parity != ParityNone:
		r.parityBit = level
	case bitNumber == stopBitNumber(r.parity):
		r.completeFrame(level, index)
	}
}

func (r *Receiver) completeFrame(stopLevel int, index uint64) {
	parityError := false
	if r.parity != ParityNone {
		ones := bits.OnesCount8(r.data) + r.parityBit
		parityError = r.parity == ParityEven && ones%2 != 0
		parityError = parityError || (r.parity == ParityOdd && ones%2 != 1)
	}

	framingError := stopLevel == 0
	breakFrame := framingError && r.data == 0 && (r.parity == ParityNone || r.parityBit == 0)
	frame := Frame{
		Byte:           r.data,
		ParityError:    parityError,
		FramingError:   framingError,
		Break:          breakFrame,
		EndSampleIndex: index,
	}

	r.produced++
	if r.count == len(r.queue) {
		r.overflows++
	} else {
		position := (r.head + r.count) % len(r.queue)
		r.queue[position] = frame
		r.count++
	}

	if framingError {
		r.state = stateWaitHigh
	} else {
		r.state = stateIdle
	}
}

func stopBitNumber(parity ParityMode) int {
	if parity == ParityNone {
		return 9
	}
	return 10
}

func (r *Receiver) lock() {
	r.mu.Lock()
}

func (r *Receiver) unlock() {
	r.mu.Unlock()
}
