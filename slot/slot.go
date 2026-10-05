// Package slot tracks the in-flight slots of an OTA campaign and the
// deadlines of the devices occupying them.
package slot

import "container/heap"

// Entry is an in-flight record popped from a Slot.
type Entry struct {
	ID string
	DL int64
}

type item struct {
	id  string
	dl  int64
	idx int
}

// pq is a min-heap ordered by (dl, id).
type pq []*item

func (q pq) Len() int { return len(q) }

func (q pq) Less(i, j int) bool {
	if q[i].dl != q[j].dl {
		return q[i].dl < q[j].dl
	}
	return q[i].id < q[j].id
}

func (q pq) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].idx = i
	q[j].idx = j
}

func (q *pq) Push(x any) {
	it := x.(*item)
	it.idx = len(*q)
	*q = append(*q, it)
}

func (q *pq) Pop() any {
	old := *q
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return it
}

// Slot is a fixed-capacity set of in-flight devices ordered by deadline.
type Slot struct {
	cap   int
	items map[string]*item
	queue pq
}

// New returns a Slot with the given capacity.
func New(capacity int) *Slot {
	return &Slot{cap: capacity, items: make(map[string]*item)}
}

// Free returns the number of unused slots.
func (s *Slot) Free() int { return s.cap - len(s.queue) }

// InFlight returns the number of occupied slots.
func (s *Slot) InFlight() int { return len(s.queue) }

// Acquire takes one slot for id with deadline dl. The caller must
// ensure Free() > 0 and that id is not already in flight.
func (s *Slot) Acquire(id string, dl int64) {
	it := &item{id: id, dl: dl}
	s.items[id] = it
	heap.Push(&s.queue, it)
}

// Release returns the slot held by id. Releasing an absent id is a no-op.
func (s *Slot) Release(id string) {
	it, ok := s.items[id]
	if !ok {
		return
	}
	delete(s.items, id)
	heap.Remove(&s.queue, it.idx)
}

// Deadline reports the deadline of an in-flight device.
func (s *Slot) Deadline(id string) (int64, bool) {
	it, ok := s.items[id]
	if !ok {
		return 0, false
	}
	return it.dl, true
}

// Expire pops and returns every entry with dl <= now, in (dl, id)
// ascending order. It pops exactly the expired entries: the first
// non-expired entry stops the scan without being popped.
func (s *Slot) Expire(now int64) []Entry {
	var out []Entry
	for len(s.queue) > 0 && s.queue[0].dl <= now {
		it := heap.Pop(&s.queue).(*item)
		delete(s.items, it.id)
		out = append(out, Entry{ID: it.id, DL: it.dl})
	}
	return out
}

// Restore puts a previously popped entry back. Used to roll back a
// tentative Expire when the enclosing operation is rejected.
func (s *Slot) Restore(e Entry) {
	s.Acquire(e.ID, e.DL)
}
