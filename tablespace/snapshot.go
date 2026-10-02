package tablespace

// ExtentView is one extent in a Snapshot. State is FREE, FRAG, FULLFRAG or
// "SEG(s)" (e.g. "SEG(3)"). Owner is the owning segment for SEG extents, 0
// otherwise. Used is the number of allocated pages in the extent.
type ExtentView struct {
	ID     int
	State  string
	Owner  int
	Used   int
	Queued bool
}

// Snapshot is a deterministic, comparable view of allocator state.
type Snapshot struct {
	X             int
	F             int
	E             int
	Pages         []int // owner segment id per page, 0 when free
	Extents       []ExtentView
	Used          map[int]int
	Alive         map[int]bool
	NextSegID     int
	NonFullQueues map[int][]int // segment id -> queued extent ids in FIFO order
}

// Snapshot captures the complete allocator state for tests and logging.
func (a *Allocator) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	snap := Snapshot{
		X:             a.x,
		F:             a.f,
		E:             a.e,
		Pages:         make([]int, a.e*a.x),
		Extents:       make([]ExtentView, a.e),
		Used:          map[int]int{},
		Alive:         map[int]bool{},
		NextSegID:     a.nextID,
		NonFullQueues: map[int][]int{},
	}

	for sid := 1; sid < len(a.segments); sid++ {
		seg := a.segments[sid]
		snap.Alive[sid] = seg.alive
		if !seg.alive {
			continue
		}
		snap.Used[sid] = seg.used
		q := []int{}
		for eid := seg.qHead; eid >= 0; eid = a.extents[eid].qNext {
			q = append(q, eid)
		}
		snap.NonFullQueues[sid] = q
	}

	for eid := 0; eid < a.e; eid++ {
		ex := &a.extents[eid]
		v := ExtentView{ID: eid, Used: ex.used}
		switch ex.state {
		case stFree:
			v.State = StateFree
		case stFrag:
			v.State = StateFrag
		case stFullFrag:
			v.State = StateFullFrag
		case stSeg:
			v.State = segStateName(ex.owner)
			v.Owner = ex.owner
		}
		v.Queued = ex.qPrev >= 0 || ex.qNext >= 0 ||
			(ex.owner != 0 && a.segments[ex.owner] != nil &&
				a.segments[ex.owner].qHead == eid)
		snap.Extents[eid] = v

		for off := 0; off < a.x; off++ {
			p := eid*a.x + off
			if a.pageBit(p) == 0 {
				continue
			}
			if ex.state == stSeg {
				snap.Pages[p] = ex.owner
			} else {
				snap.Pages[p] = int(a.fragOwners[eid][off])
			}
		}
	}
	return snap
}

func segStateName(owner int) string {
	return "SEG(" + itoa(owner) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
