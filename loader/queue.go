package loader

import (
	"container/heap"
	"sort"
)

// pdeque is a queue of pending requests kept sorted by registration seq.
type pdeque struct {
	items []*request
}

func (d *pdeque) pushBack(r *request) { d.items = append(d.items, r) }

// insertBySeq files a paused request back by its original registration
// order, never by pause time.
func (d *pdeque) insertBySeq(r *request) {
	i := sort.Search(len(d.items), func(i int) bool { return d.items[i].seq > r.seq })
	d.items = append(d.items, nil)
	copy(d.items[i+1:], d.items[i:])
	d.items[i] = r
}

func (d *pdeque) front() *request {
	if len(d.items) == 0 {
		return nil
	}
	return d.items[0]
}

func (d *pdeque) remove(r *request) {
	for i, x := range d.items {
		if x == r {
			d.items = append(d.items[:i], d.items[i+1:]...)
			return
		}
	}
}

// readyItem is an origin's entry in a tier heap, keyed by the seq of the
// origin's oldest pending request in that tier.
type readyItem struct {
	origin  Origin
	headSeq uint64
	index   int
}

type readyHeap []*readyItem

func (h readyHeap) Len() int { return len(h) }

func (h readyHeap) Less(i, j int) bool { return h[i].headSeq < h[j].headSeq }

func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *readyHeap) Push(x any) {
	it := x.(*readyItem)
	it.index = len(*h)
	*h = append(*h, it)
}

func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

type queueKey struct {
	tier   int
	origin Origin
}

// pendingSet stores pending requests in per-(tier, origin) seq-ordered
// deques and indexes deque heads with heaps:
//
//   - ready[tier]: origins with spare per-origin capacity (start candidates)
//   - top: every origin with highest-tier pending requests, regardless of
//     capacity (preemption candidates)
//
// peek and peekTop read heap roots, so selecting the next startable
// request is O(1); mutations cost O(log #origins). Neither grows with the
// total number of pending requests.
type pendingSet struct {
	deques     [numPriorities]map[Origin]*pdeque
	ready      [numPriorities]readyHeap
	top        readyHeap
	members    map[queueKey]*readyItem
	topMembers map[Origin]*readyItem
}

func newPendingSet() *pendingSet {
	ps := &pendingSet{
		members:    make(map[queueKey]*readyItem),
		topMembers: make(map[Origin]*readyItem),
	}
	for i := range ps.deques {
		ps.deques[i] = make(map[Origin]*pdeque)
	}
	return ps
}

// add files a request under its effective-priority tier; paused requests
// keep their original registration order.
func (ps *pendingSet) add(r *request, paused bool) {
	tier := int(r.effPriority())
	r.tier = tier
	dq := ps.deques[tier][r.in.Origin]
	if dq == nil {
		dq = &pdeque{}
		ps.deques[tier][r.in.Origin] = dq
	}
	if paused {
		dq.insertBySeq(r)
	} else {
		dq.pushBack(r)
	}
}

func (ps *pendingSet) remove(r *request) {
	if dq := ps.deques[r.tier][r.in.Origin]; dq != nil {
		dq.remove(r)
	}
}

// fix refreshes the heap membership of one origin in one tier. It must be
// called after any change to that origin's deque or spare capacity.
func (ps *pendingSet) fix(tier int, origin Origin, spare bool) {
	key := queueKey{tier, origin}
	var head *request
	if dq := ps.deques[tier][origin]; dq != nil {
		head = dq.front()
	}
	it, ok := ps.members[key]
	switch {
	case head != nil && spare:
		if ok {
			if it.headSeq != head.seq {
				it.headSeq = head.seq
				heap.Fix(&ps.ready[tier], it.index)
			}
		} else {
			it = &readyItem{origin: origin, headSeq: head.seq}
			heap.Push(&ps.ready[tier], it)
			ps.members[key] = it
		}
	case ok:
		heap.Remove(&ps.ready[tier], it.index)
		delete(ps.members, key)
	}
	if tier == int(PriorityHighest) {
		it, ok := ps.topMembers[origin]
		switch {
		case head != nil:
			if ok {
				if it.headSeq != head.seq {
					it.headSeq = head.seq
					heap.Fix(&ps.top, it.index)
				}
			} else {
				it = &readyItem{origin: origin, headSeq: head.seq}
				heap.Push(&ps.top, it)
				ps.topMembers[origin] = it
			}
		case ok:
			heap.Remove(&ps.top, it.index)
			delete(ps.topMembers, origin)
		}
	}
}

// peek returns the next startable request: highest tier first, then the
// smallest registration seq among origins with spare capacity.
func (ps *pendingSet) peek() *request {
	for p := int(PriorityHighest); p >= 0; p-- {
		if len(ps.ready[p]) > 0 {
			it := ps.ready[p][0]
			return ps.deques[p][it.origin].front()
		}
	}
	return nil
}

// peekTop returns the earliest registered pending highest-priority
// request, regardless of origin capacity (preemption candidate).
func (ps *pendingSet) peekTop() *request {
	if len(ps.top) == 0 {
		return nil
	}
	it := ps.top[0]
	return ps.deques[int(PriorityHighest)][it.origin].front()
}

// syncTier re-files a pending request after its effective priority changed
// (attachment or detach). Seq order inside the new tier is preserved.
func (ps *pendingSet) syncTier(r *request) {
	if r.state != StatePending {
		return
	}
	if want := int(r.effPriority()); want != r.tier {
		ps.remove(r)
		ps.add(r, true)
	}
}
