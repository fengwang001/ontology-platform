// Package reuse schedules suppressed routes by their reuse instant.
//
// The ordering key is (ReuseAt, Peer, Prefix). Reuse is a pure function of
// time: due items are popped by the damp state machine at the start of each
// accepted operation, so replaying the same operation sequence yields the
// same event sequence.
package reuse

// Key identifies a route.
type Key struct {
	Peer   int64
	Prefix int64
}

// Entry is one scheduled reuse item. The heap index is kept inside the entry
// so an entry that is rescheduled while still in the heap can be repaired in
// place via Fix.
type Entry struct {
	Key
	ReuseAt int64
	index   int
}

// Scheduler is a min-heap of suppressed routes.
type Scheduler struct {
	items []*Entry
}

// New creates an empty Scheduler.
func New() *Scheduler { return &Scheduler{} }

// Len reports the number of scheduled entries.
func (s *Scheduler) Len() int { return len(s.items) }

// Add inserts a new entry into the schedule.
func (s *Scheduler) Add(e *Entry) {
	e.index = len(s.items)
	s.items = append(s.items, e)
	s.up(e.index)
}

// Remove deletes an entry from the schedule.
func (s *Scheduler) Remove(e *Entry) {
	i := e.index
	if i < 0 || i >= len(s.items) || s.items[i] != e {
		return
	}
	n := len(s.items) - 1
	if i != n {
		s.swap(i, n)
		s.items = s.items[:n]
		if i < n {
			s.down(i)
			s.up(i)
		}
	} else {
		s.items = s.items[:n]
	}
	e.index = -1
}

// PopDue removes and returns every entry with ReuseAt <= now, in scheduling
// order (ReuseAt, Peer, Prefix).
func (s *Scheduler) PopDue(now int64) []*Entry {
	var due []*Entry
	for len(s.items) > 0 && s.items[0].ReuseAt <= now {
		n := len(s.items) - 1
		e := s.items[0]
		s.swap(0, n)
		s.items = s.items[:n]
		if n > 0 {
			s.down(0)
		}
		e.index = -1
		due = append(due, e)
	}
	return due
}

func (s *Scheduler) less(i, j int) bool {
	a, b := s.items[i], s.items[j]
	if a.ReuseAt != b.ReuseAt {
		return a.ReuseAt < b.ReuseAt
	}
	if a.Peer != b.Peer {
		return a.Peer < b.Peer
	}
	return a.Prefix < b.Prefix
}

func (s *Scheduler) swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
	s.items[i].index = i
	s.items[j].index = j
}

func (s *Scheduler) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !s.less(i, parent) {
			return
		}
		s.swap(i, parent)
		i = parent
	}
}

func (s *Scheduler) down(i int) {
	n := len(s.items)
	for {
		smallest := i
		l := 2*i + 1
		r := l + 1
		if l < n && s.less(l, smallest) {
			smallest = l
		}
		if r < n && s.less(r, smallest) {
			smallest = r
		}
		if smallest == i {
			return
		}
		s.swap(i, smallest)
		i = smallest
	}
}
