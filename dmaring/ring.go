// Package dmaring implements a host-side driver model of a DMA descriptor
// ring with OWN-bit ownership handoff between host and a simulated device.
package dmaring

import (
	"errors"
	"sync"
)

var (
	ErrInvalidSlotCount = errors.New("dmaring: slot count must be a power of two and >= 2")
	ErrEmptyLens        = errors.New("dmaring: segment length list is empty")
	ErrNonPositiveLen   = errors.New("dmaring: segment length must be > 0")
	ErrTooManySegments  = errors.New("dmaring: segment count exceeds ring capacity N")
	ErrInsufficientFree = errors.New("dmaring: segment count exceeds current Free")
	ErrNegativeK        = errors.New("dmaring: DeviceRun k must be >= 0")
	ErrFaultBeforeDev   = errors.New("dmaring: fault sequence number is below dev")
	ErrNoPacket         = errors.New("dmaring: no packet to reap (reap == prod)")
	ErrPacketIncomplete = errors.New("dmaring: packet still has descriptors owned by device")
)

// Slot is one descriptor in the ring.
type Slot struct {
	Own   bool // true: owned by device, false: owned by host
	First bool // first descriptor of a packet
	Last  bool // last descriptor of a packet
	Len   int
	St    int // 0 fresh, 1 ok, 2 fault head, 3 cascaded discard
}

// ReapResult describes one reaped packet.
type ReapResult struct {
	Segments int
	TotalLen int
	ErrIndex int // index within the packet of the st==2 segment, -1 if none
}

// Ring is the DMA descriptor ring model. All methods are safe for
// concurrent use; results are equivalent to some serial order.
type Ring struct {
	mu     sync.Mutex
	n      int
	slots  []Slot
	prod   uint64 // next submit position (unbounded)
	dev    uint64 // next device process position (unbounded)
	reap   uint64 // next host reap position (unbounded)
	faults map[uint64]struct{}
}

// NewRing creates a ring with n slots; n must be a power of two and >= 2.
func NewRing(n int) (*Ring, error) {
	if n < 2 || n&(n-1) != 0 {
		return nil, ErrInvalidSlotCount
	}
	return &Ring{
		n:      n,
		slots:  make([]Slot, n),
		faults: make(map[uint64]struct{}),
	}, nil
}

// Free returns N - (prod - reap).
func (r *Ring) Free() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n - int(r.prod-r.reap)
}

// Pointers returns the unbounded (prod, dev, reap) pointers.
func (r *Ring) Pointers() (prod, dev, reap uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prod, r.dev, r.reap
}

// Submit writes one packet of len(lens) segments starting at prod.
// The whole packet is written atomically or not at all.
func (r *Ring) Submit(lens []int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(lens) == 0 {
		return ErrEmptyLens
	}
	for _, l := range lens {
		if l <= 0 {
			return ErrNonPositiveLen
		}
	}
	if len(lens) > r.n {
		return ErrTooManySegments
	}
	if free := r.n - int(r.prod-r.reap); len(lens) > free {
		return ErrInsufficientFree
	}
	for i, l := range lens {
		s := &r.slots[(r.prod+uint64(i))%uint64(r.n)]
		s.Own = true
		s.First = i == 0
		s.Last = i == len(lens)-1
		s.Len = l
		s.St = 0
	}
	r.prod += uint64(len(lens))
	return nil
}

// DeviceRun completes at most k processing units and returns the
// actual number completed.
func (r *Ring) DeviceRun(k int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k < 0 {
		return 0, ErrNegativeK
	}
	done := 0
	for done < k && r.dev < r.prod && r.slots[r.dev%uint64(r.n)].Own {
		if _, bad := r.faults[r.dev]; bad {
			r.cascade()
		} else {
			s := &r.slots[r.dev%uint64(r.n)]
			s.Own = false
			s.St = 1
			r.dev++
		}
		done++
	}
	return done, nil
}

// cascade processes the faulted descriptor at r.dev: it becomes st=2 and
// every following segment of the same packet (up to and including LAST)
// is discarded with st=3. Fault registrations on discarded segments are
// voided. The whole cascade costs a single processing unit.
func (r *Ring) cascade() {
	delete(r.faults, r.dev)
	s := &r.slots[r.dev%uint64(r.n)]
	s.Own = false
	s.St = 2
	last := s.Last
	r.dev++
	for !last {
		s := &r.slots[r.dev%uint64(r.n)]
		s.Own = false
		s.St = 3
		last = s.Last
		delete(r.faults, r.dev)
		r.dev++
	}
}

// Fault registers that the descriptor with unbounded sequence number
// seq will fail when the device processes it.
func (r *Ring) Fault(seq uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq < r.dev {
		return ErrFaultBeforeDev
	}
	r.faults[seq] = struct{}{}
	return nil
}

// Reap reclaims one whole packet starting at reap.
func (r *Ring) Reap() (ReapResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reap == r.prod {
		return ReapResult{}, ErrNoPacket
	}
	res := ReapResult{ErrIndex: -1}
	for seq := r.reap; ; seq++ {
		s := r.slots[seq%uint64(r.n)]
		if s.Own {
			return ReapResult{}, ErrPacketIncomplete
		}
		res.Segments++
		res.TotalLen += s.Len
		if s.St == 2 {
			res.ErrIndex = res.Segments - 1
		}
		if s.Last {
			break
		}
	}
	r.reap += uint64(res.Segments)
	return res, nil
}
