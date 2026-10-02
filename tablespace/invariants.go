package tablespace

import "fmt"

// CheckInvariants verifies every documented invariant and returns a
// descriptive error if any is violated.
func (a *Allocator) CheckInvariants() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	totalPages := a.e * a.x
	allocated := 0
	seenQueued := map[int]map[int]bool{}
	ownerTotals := map[int]int{}

	for sid := 1; sid < len(a.segments); sid++ {
		seg := a.segments[sid]
		if !seg.alive {
			continue
		}
		seen := map[int]bool{}
		prev := -1
		count := 0
		for eid := seg.qHead; eid >= 0; count++ {
			if seen[eid] {
				return fmt.Errorf("segment %d: extent %d duplicated in queue", sid, eid)
			}
			seen[eid] = true
			ex := &a.extents[eid]
			if ex.state != stSeg || ex.owner != sid {
				return fmt.Errorf("segment %d: queued extent %d is not its SEG extent", sid, eid)
			}
			if ex.used <= 0 || ex.used >= a.x {
				return fmt.Errorf("segment %d: queued extent %d used=%d not in 1..x-1", sid, eid, ex.used)
			}
			if a.extents[eid].qPrev != prev {
				return fmt.Errorf("segment %d: broken prev link at extent %d", sid, eid)
			}
			if count > a.e+1 {
				return fmt.Errorf("segment %d: queue cycle", sid)
			}
			prev = eid
			eid = a.extents[eid].qNext
		}
		if (count == 0) != (seg.qHead == -1) || (count == 0) != (seg.qTail == -1) {
			return fmt.Errorf("segment %d: inconsistent queue head/tail", sid)
		}
		seenQueued[sid] = seen
	}

	for eid := 0; eid < a.e; eid++ {
		ex := &a.extents[eid]
		count := 0
		for off := 0; off < a.x; off++ {
			p := eid*a.x + off
			if a.pageBit(p) != 0 {
				count++
			}
			switch ex.state {
			case stSeg:
				if a.pageBit(p) != 0 {
					ownerTotals[ex.owner]++
				}
			case stFrag, stFullFrag:
				if o := int(a.fragOwners[eid][off]); a.pageBit(p) != 0 {
					if o < 1 {
						return fmt.Errorf("extent %d: allocated page %d has no fragment owner", eid, p)
					}
					ownerTotals[o]++
				} else if o != 0 {
					return fmt.Errorf("extent %d: free page %d still records owner", eid, p)
				}
			}
		}
		if count != ex.used {
			return fmt.Errorf("extent %d: bitmap count %d != used %d", eid, count, ex.used)
		}
		allocated += count

		switch ex.state {
		case stFree:
			if ex.used != 0 || ex.owner != 0 {
				return fmt.Errorf("extent %d: FREE extent dirty used=%d owner=%d", eid, ex.used, ex.owner)
			}
		case stFrag:
			if ex.used < 1 || ex.used > a.x-1 {
				return fmt.Errorf("extent %d: FRAG used=%d out of 1..x-1", eid, ex.used)
			}
		case stFullFrag:
			if ex.used != a.x {
				return fmt.Errorf("extent %d: FULLFRAG used=%d != x", eid, ex.used)
			}
		case stSeg:
			if ex.used < 1 || ex.used > a.x {
				return fmt.Errorf("extent %d: SEG used=%d out of 1..x", eid, ex.used)
			}
			shouldQueue := ex.used < a.x
			isQueued := seenQueued[ex.owner][eid]
			if shouldQueue != isQueued {
				return fmt.Errorf("extent %d: queue membership wrong (want %v got %v)", eid, shouldQueue, isQueued)
			}
		}
	}

	usedSum := 0
	for sid := 1; sid < len(a.segments); sid++ {
		if a.segments[sid].alive {
			usedSum += a.segments[sid].used
			if ownerTotals[sid] != a.segments[sid].used {
				return fmt.Errorf("segment %d: used %d != owned pages %d", sid, a.segments[sid].used, ownerTotals[sid])
			}
		}
	}
	if usedSum != allocated {
		return fmt.Errorf("sum of used %d != allocated pages %d", usedSum, allocated)
	}
	if usedSum+(totalPages-allocated) != totalPages {
		return fmt.Errorf("used + free != total pages")
	}
	return nil
}
