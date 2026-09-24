package drr

func (s *Scheduler) try(f *flow, n int) (int, int, bool) {
	if !s.current {
		f.deficit += f.quantum
	}
	size, ok := f.q.Front()
	if !ok || size > f.deficit {
		s.head, s.current = (s.head+1)%n, false
		return 0, 0, false
	}
	f.q.Pop()
	f.deficit, f.queued, f.sent, s.queued = f.deficit-size, f.queued-1, f.sent+size, s.queued-1
	id := f.id
	if f.q.Len() > 0 {
		s.current = true
		return id, size, true
	}
	f.deficit, s.current = 0, false
	s.active = append(s.active[:s.head], s.active[s.head+1:]...)
	if len(s.active) > 0 {
		s.head %= len(s.active)
	}
	return id, size, true
}

func (s *Scheduler) Dequeue() (int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks = 0
	n := len(s.active)
	for range n {
		f := s.flows[s.active[s.head]]
		s.checks++
		if id, size, ok := s.try(f, n); ok {
			return id, size, true
		}
	}
	return 0, 0, false
}
