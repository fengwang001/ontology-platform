package tablespace

import (
	"container/heap"
)

// allocFrag performs a fragment allocation, ignoring hint.
func (a *Allocator) allocFrag(seg *segData, sid int) (int, error) {
	eid := a.smallestFragExtent()
	if eid < 0 {
		eid = a.smallestFreeExtent()
		if eid < 0 {
			return 0, ErrNoSpace
		}
		ex := &a.extents[eid]
		ex.state = stFrag
		ex.qPrev, ex.qNext = -1, -1
		a.fragOwners[eid] = make([]int32, a.x)
		a.fragExtents.PushID(eid)
	}

	ex := &a.extents[eid]
	off := a.firstFreeOffset(eid)
	p := eid*a.x + off
	a.setPageBit(p)
	a.fragOwners[eid][off] = int32(sid)
	ex.used++
	seg.used++
	if ex.used == a.x {
		ex.state = stFullFrag
	}
	return p, nil
}

// allocSeg performs an exclusive-mode allocation: hint first, then the
// non-full queue head, then a fresh FREE extent (hint ignored there).
func (a *Allocator) allocSeg(seg *segData, sid, hint int) (int, error) {
	if hint >= 0 {
		he := hint / a.x
		hex := &a.extents[he]
		if hex.state == stSeg && hex.owner == sid && a.pageBit(hint) == 0 {
			a.setPageBit(hint)
			hex.used++
			seg.used++
			if hex.used == a.x {
				a.unqueue(seg, he)
			}
			return hint, nil
		}
	}

	if seg.qHead >= 0 {
		eid := seg.qHead
		ex := &a.extents[eid]
		off := a.firstFreeOffset(eid)
		p := eid*a.x + off
		a.setPageBit(p)
		ex.used++
		seg.used++
		if ex.used == a.x {
			a.unqueue(seg, eid)
		}
		return p, nil
	}

	eid := a.smallestFreeExtent()
	if eid < 0 {
		return 0, ErrNoSpace
	}
	ex := &a.extents[eid]
	ex.state = stSeg
	ex.owner = sid
	ex.qPrev, ex.qNext = -1, -1
	seg.exclusive = append(seg.exclusive, eid)

	off := a.firstFreeOffset(eid)
	p := eid*a.x + off
	a.setPageBit(p)
	ex.used = 1
	seg.used++
	a.enqueueTail(seg, eid)
	return p, nil
}

// releasePage frees one page at (eid, off), already known to belong to
// seg/sid, migrating extent state and the non-full queue per the rules.
func (a *Allocator) releasePage(seg *segData, sid, eid, off int) {
	ex := &a.extents[eid]
	p := eid*a.x + off

	switch ex.state {
	case stFullFrag:
		a.clearPageBit(p)
		a.fragOwners[eid][off] = 0
		ex.used--
		seg.used--
		ex.state = stFrag
		// It may have been lazily evicted from the heap while FULLFRAG.
		a.fragExtents.PushID(eid)
		if ex.used == 0 {
			ex.state = stFree
			a.fragOwners[eid] = nil
			a.freeExtents.PushID(eid)
		}
	case stFrag:
		a.clearPageBit(p)
		a.fragOwners[eid][off] = 0
		ex.used--
		seg.used--
		if ex.used == 0 {
			ex.state = stFree
			a.fragOwners[eid] = nil
			a.freeExtents.PushID(eid)
		}
	case stSeg:
		wasFull := ex.used == a.x
		a.clearPageBit(p)
		ex.used--
		seg.used--
		if ex.used == 0 {
			a.unqueue(seg, eid)
			ex.state = stFree
			ex.owner = 0
			a.removeExclusive(seg, eid)
			a.freeExtents.PushID(eid)
		} else if wasFull {
			a.enqueueTail(seg, eid)
		}
	}
}

func (a *Allocator) removeExclusive(seg *segData, eid int) {
	for i, v := range seg.exclusive {
		if v == eid {
			seg.exclusive = append(seg.exclusive[:i], seg.exclusive[i+1:]...)
			return
		}
	}
}

// enqueueTail appends eid to seg's non-full queue. The caller guarantees it
// is not already queued.
func (a *Allocator) enqueueTail(seg *segData, eid int) {
	ex := &a.extents[eid]
	ex.qPrev = seg.qTail
	ex.qNext = -1
	if seg.qTail >= 0 {
		a.extents[seg.qTail].qNext = eid
	} else {
		seg.qHead = eid
	}
	seg.qTail = eid
}

// unqueue removes eid from seg's non-full queue if it is queued.
func (a *Allocator) unqueue(seg *segData, eid int) {
	ex := &a.extents[eid]
	if seg.qHead != eid && ex.qPrev < 0 && ex.qNext < 0 {
		return
	}
	if ex.qPrev >= 0 {
		a.extents[ex.qPrev].qNext = ex.qNext
	} else {
		seg.qHead = ex.qNext
	}
	if ex.qNext >= 0 {
		a.extents[ex.qNext].qPrev = ex.qPrev
	} else {
		seg.qTail = ex.qPrev
	}
	ex.qPrev = -1
	ex.qNext = -1
}

// smallestFragExtent returns the smallest non-full FRAG extent id.
func (a *Allocator) smallestFragExtent() int {
	h := &a.fragExtents
	for h.Len() > 0 {
		id := (*h)[0]
		ex := &a.extents[id]
		if ex.state == stFrag && ex.used < a.x {
			return id
		}
		heap.Pop(h)
	}
	return -1
}

// smallestFreeExtent returns the smallest FREE extent id.
func (a *Allocator) smallestFreeExtent() int {
	h := &a.freeExtents
	for h.Len() > 0 {
		id := (*h)[0]
		if a.extents[id].state == stFree {
			return id
		}
		heap.Pop(h)
	}
	return -1
}

func (a *Allocator) firstFreeOffset(eid int) int {
	base := eid * a.x
	for off := 0; off < a.x; off++ {
		if a.pageBit(base+off) == 0 {
			return off
		}
	}
	return -1
}

func (a *Allocator) pageBit(p int) uint64 {
	return (a.pages[p>>6] >> uint(p&63)) & 1
}

func (a *Allocator) setPageBit(p int) {
	a.pages[p>>6] |= 1 << uint(p&63)
}

func (a *Allocator) clearPageBit(p int) {
	a.pages[p>>6] &^= 1 << uint(p&63)
}
