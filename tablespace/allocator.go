package tablespace

import (
	"container/heap"
	"sync"
)

// Human readable extent states, exposed via the debug snapshot.
const (
	StateFree     = "FREE"
	StateFrag     = "FRAG"
	StateFullFrag = "FULLFRAG"
)

const (
	stFree int8 = iota
	stFrag
	stFullFrag
	stSeg
)

type extent struct {
	state int8
	owner int // owning segment id for stSeg, 0 otherwise
	used  int
	qPrev int // non-full queue linked-list node; -1 when not queued
	qNext int
}

type segData struct {
	alive     bool
	used      int
	qHead     int // non-full exclusive-extent queue (FIFO)
	qTail     int
	exclusive []int
}

// Allocator is a tablespace page allocator with shared fragment extents and
// per-segment exclusive extents. All operations are safe for concurrent use
// and linearized by a single internal mutex.
type Allocator struct {
	mu sync.Mutex

	x int
	f int
	e int

	extents    []extent
	pages      []uint64 // one bit per page; 1 means allocated
	fragOwners [][]int32

	segments []*segData // 1-based; index 0 unused
	nextID   int

	freeExtents minExtentHeap // lazily cleaned
	fragExtents minExtentHeap // lazily cleaned
}

// New creates an Allocator: x pages per extent (2..1024), fragment threshold
// f (1..x), and e extents (1..100000).
func New(x, f, e int) (*Allocator, error) {
	if x < 2 || x > 1024 || f < 1 || f > x || e < 1 || e > 100000 {
		return nil, ErrInvalidArgument
	}
	a := &Allocator{
		x:           x,
		f:           f,
		e:           e,
		extents:     make([]extent, e),
		pages:       make([]uint64, (e*x+63)/64),
		fragOwners:  make([][]int32, e),
		segments:    []*segData{nil},
		nextID:      1,
		freeExtents: make(minExtentHeap, 0, e),
		fragExtents: make(minExtentHeap, 0, e),
	}
	for i := range a.extents {
		a.extents[i].qPrev = -1
		a.extents[i].qNext = -1
		a.freeExtents.PushID(i)
	}
	heap.Init(&a.freeExtents)
	return a, nil
}

// NewSegment creates a segment and returns its 1-based, never-reused id.
func (a *Allocator) NewSegment() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextID
	a.nextID++
	a.segments = append(a.segments, &segData{alive: true, qHead: -1, qTail: -1})
	return id
}

// Used returns the number of pages owned by segment s.
func (a *Allocator) Used(s int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s >= len(a.segments) || !a.segments[s].alive {
		return 0, ErrSegmentNotFound
	}
	return a.segments[s].used, nil
}

// AllocPage allocates one page for segment s. hint is -1 or a page number;
// it is honored only inside a non-full exclusive extent already owned by s.
func (a *Allocator) AllocPage(s int, hint int) (int, error) {
	if hint < -1 || hint >= a.e*a.x {
		return 0, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s >= len(a.segments) || !a.segments[s].alive {
		return 0, ErrSegmentNotFound
	}
	seg := a.segments[s]
	if seg.used < a.f {
		return a.allocFrag(seg, s)
	}
	return a.allocSeg(seg, s, hint)
}

// FreePage frees page p; p must currently be allocated and owned by s.
func (a *Allocator) FreePage(s, p int) error {
	if p < 0 || p >= a.e*a.x {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s >= len(a.segments) || !a.segments[s].alive {
		return ErrSegmentNotFound
	}
	eid, off := p/a.x, p%a.x
	ex := &a.extents[eid]
	switch ex.state {
	case stFrag, stFullFrag:
		if int(a.fragOwners[eid][off]) != s {
			return ErrPageNotOwned
		}
	case stSeg:
		if ex.owner != s || a.pageBit(p) == 0 {
			return ErrPageNotOwned
		}
	default:
		return ErrPageNotOwned
	}
	a.releasePage(a.segments[s], s, eid, off)
	return nil
}

// FreeSegment releases every page and extent owned by segment s; the id
// becomes invalid and is never reused.
func (a *Allocator) FreeSegment(s int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s >= len(a.segments) || !a.segments[s].alive {
		return ErrSegmentNotFound
	}
	seg := a.segments[s]

	for eid := range a.fragOwners {
		owners := a.fragOwners[eid]
		if len(owners) == 0 {
			continue
		}
		if st := a.extents[eid].state; st != stFrag && st != stFullFrag {
			continue
		}
		for off := 0; off < a.x; off++ {
			if int(owners[off]) == s {
				a.releasePage(seg, s, eid, off)
			}
		}
	}

	for _, eid := range seg.exclusive {
		ex := &a.extents[eid]
		for off := 0; off < a.x; off++ {
			p := eid*a.x + off
			if a.pageBit(p) != 0 {
				a.clearPageBit(p)
				ex.used--
				seg.used--
			}
		}
		a.unqueue(seg, eid)
		ex.state = stFree
		ex.owner = 0
		a.freeExtents.PushID(eid)
	}
	seg.exclusive = seg.exclusive[:0]
	seg.alive = false
	seg.used = 0
	seg.qHead, seg.qTail = -1, -1
	return nil
}
