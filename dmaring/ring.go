// Package dmaring implements a host-side DMA descriptor ring model.
//
// The ring has N slots (N is a power of two and N >= 2) and three
// monotonically increasing, never-wrapping pointers: prod (next submit
// position), dev (next device-process position) and reap (next host-reap
// position). The physical slot of a pointer value p is p % N.
package dmaring

import "sync"

// Slot status values.
const (
	StInit  = 0 // descriptor not processed yet
	StDone  = 1 // descriptor processed normally
	StFault = 2 // descriptor faulted at device processing
	StDrop  = 3 // descriptor discarded as part of a faulted packet
)

// Sentinel errors. Each rejected operation reports a distinguishable cause.
var (
	ErrBadN             = errStr("dmaring: N must be a power of two and >= 2")
	ErrEmptyLens        = errStr("dmaring: submit: lens is empty")
	ErrBadLen           = errStr("dmaring: submit: each segment length must be > 0")
	ErrSegmentsOverN    = errStr("dmaring: submit: segment count exceeds N")
	ErrNoFreeSlots      = errStr("dmaring: submit: segment count exceeds free slots")
	ErrNegativeK        = errStr("dmaring: devicerun: k must not be negative")
	ErrFaultBeforeDev   = errStr("dmaring: fault: seq must not be less than dev")
	ErrNoPacket         = errStr("dmaring: reap: no packet to reap")
	ErrPacketIncomplete = errStr("dmaring: reap: packet still owned by device")
)

type errStr string

func (e errStr) Error() string { return string(e) }

// Slot is a single descriptor slot.
type Slot struct {
	OWN   int // 1 means the slot belongs to the device
	FIRST int // 1 marks the first segment of a packet
	LAST  int // 1 marks the last segment of a packet
	Len   int64
	St    int
}

// Ring is the DMA descriptor ring.
type Ring struct {
	mu sync.Mutex
	n  int

	slots []Slot

	// Unbounded pointers (never wrap; physical slot is the value mod N).
	prod uint64
	dev  uint64
	reap uint64

	// Registered fault descriptor sequence numbers.
	faults map[uint64]struct{}
}

// ReapResult describes one reaped packet.
type ReapResult struct {
	Segments   int
	TotalLen   int64
	FaultIndex int // packet-local index of the st==2 segment, -1 if none
}

// Snapshot is a point-in-time copy of the ring state.
type Snapshot struct {
	N     int
	Slots []Slot
	Prod  uint64
	Dev   uint64
	Reap  uint64
	Free  int
}

// New creates a ring of N slots.
func New(n int) (*Ring, error) {
	if n < 2 || n&(n-1) != 0 {
		return nil, ErrBadN
	}
	r := &Ring{
		n:      n,
		slots:  make([]Slot, n),
		faults: make(map[uint64]struct{}),
	}
	return r, nil
}

// Submit atomically submits one scatter-gather packet.
func (r *Ring) Submit(lens []int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Validation order: empty list, bad length, count > N, count > Free.
	// The first failing condition is the only one reported.
	m := len(lens)
	if m == 0 {
		return ErrEmptyLens
	}
	for _, ln := range lens {
		if ln <= 0 {
			return ErrBadLen
		}
	}
	if m > r.n {
		return ErrSegmentsOverN
	}
	free := r.freeLocked()
	if m > free {
		return ErrNoFreeSlots
	}
	// All checks passed: write the whole packet or nothing.
	start := r.prod
	for i := 0; i < m; i++ {
		s := &r.slots[(start+uint64(i))%uint64(r.n)]
		*s = Slot{
			OWN:   1,
			FIRST: boolToInt(i == 0),
			LAST:  boolToInt(i == m-1),
			Len:   lens[i],
			St:    StInit,
		}
	}
	r.prod = start + uint64(m)
	return nil
}

// DeviceRun lets the simulated device process at most k work units.
func (r *Ring) DeviceRun(k int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k < 0 {
		return 0, ErrNegativeK
	}
	done := 0
	for done < k {
		if r.dev >= r.prod {
			break // no descriptor owned by the device
		}
		s := &r.slots[r.dev%uint64(r.n)]
		if s.OWN == 0 {
			break
		}
		seq := r.dev
		if _, bad := r.faults[seq]; bad {
			// The faulted descriptor itself is st=2.
			s.OWN = 0
			s.St = StFault
			delete(r.faults, seq)
			r.dev = seq + 1
			// The rest of the same packet, up to and including its
			// LAST segment, is discarded with st=3. A discard never
			// overrides the st=2 head.
			if s.LAST == 0 {
				for {
					t := &r.slots[r.dev%uint64(r.n)]
					t.OWN = 0
					t.St = StDrop
					// Any fault registered on a discarded segment is voided.
					delete(r.faults, r.dev)
					last := t.LAST == 1
					r.dev++
					if last {
						break
					}
				}
			}
		} else {
			s.OWN = 0
			s.St = StDone
			r.dev = seq + 1
		}
		// A normal descriptor and a whole fault cascade each cost one unit.
		done++
	}
	return done, nil
}

// Fault registers a fault for descriptor sequence seq.
func (r *Ring) Fault(seq uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq < r.dev {
		return ErrFaultBeforeDev
	}
	r.faults[seq] = struct{}{}
	return nil
}

// Reap reaps one complete packet from reap.
func (r *Ring) Reap() (ReapResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reap >= r.prod {
		return ReapResult{}, ErrNoPacket
	}
	// Locate the packet: FIRST at reap, scan to its LAST.
	start := r.reap
	idx := uint64(0)
	for {
		s := &r.slots[(start+idx)%uint64(r.n)]
		if s.OWN != 0 {
			return ReapResult{}, ErrPacketIncomplete
		}
		if s.LAST == 1 {
			break
		}
		idx++
	}
	m := int(idx) + 1
	res := ReapResult{Segments: m, FaultIndex: -1}
	for i := 0; i < m; i++ {
		s := &r.slots[(start+uint64(i))%uint64(r.n)]
		res.TotalLen += s.Len
		if s.St == StFault {
			res.FaultIndex = i
		}
	}
	r.reap = start + uint64(m)
	return res, nil
}

// Free returns the number of free slots: N - (prod - reap).
func (r *Ring) Free() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.freeLocked()
}

func (r *Ring) freeLocked() int {
	return r.n - int(r.prod-r.reap)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Prod returns the unbounded producer pointer.
func (r *Ring) Prod() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prod
}

// Dev returns the unbounded device pointer.
func (r *Ring) Dev() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dev
}

// ReapPtr returns the unbounded reap pointer.
func (r *Ring) ReapPtr() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reap
}

// Snapshot returns a consistent copy of the ring state.
func (r *Ring) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Slot, len(r.slots))
	copy(cp, r.slots)
	return Snapshot{
		N:     r.n,
		Slots: cp,
		Prod:  r.prod,
		Dev:   r.dev,
		Reap:  r.reap,
		Free:  r.freeLocked(),
	}
}
