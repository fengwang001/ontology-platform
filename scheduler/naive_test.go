package scheduler

// naiveScheduler is a deliberately simple reference implementation of the
// spec: it linearly scans all 40 queues on dispatch and uses plain slices
// as FIFO queues. It exists only to cross-check the bitmap scheduler.

type naiveTask struct {
	id, nice, sp int
	s, prio      int
	tsLeft       int
	state        State
	sleepStart   int
}

type naiveScheduler struct {
	n         int
	now       int
	tasks     map[int]*naiveTask
	sleeping  int
	active    [NumPrios][]int
	expired   [NumPrios][]int
	cur       int
	hasCur    bool
	expiredTs int
}

func newNaive(n int) *naiveScheduler {
	return &naiveScheduler{n: n, tasks: make(map[int]*naiveTask)}
}

func naiveLen(a *[NumPrios][]int) int {
	n := 0
	for i := 0; i < NumPrios; i++ {
		n += len(a[i])
	}
	return n
}

func (s *naiveScheduler) dispatch() {
	if naiveLen(&s.active) == 0 {
		if naiveLen(&s.expired) == 0 {
			s.hasCur = false
			return
		}
		s.active, s.expired = s.expired, s.active
		s.expiredTs = 0
	}
	for i := 0; i < NumPrios; i++ {
		if len(s.active[i]) > 0 {
			id := s.active[i][0]
			s.active[i] = s.active[i][1:]
			s.tasks[id].state = Running
			s.cur = id
			s.hasCur = true
			return
		}
	}
	s.hasCur = false
}

func (s *naiveScheduler) arrive(t *naiveTask) {
	t.state = Queued
	s.active[t.prio-BasePrio] = append(s.active[t.prio-BasePrio], t.id)
	if !s.hasCur {
		s.dispatch()
		return
	}
	if t.prio < s.tasks[s.cur].prio {
		c := s.tasks[s.cur]
		s.hasCur = false
		c.state = Queued
		idx := c.prio - BasePrio
		s.active[idx] = append([]int{c.id}, s.active[idx]...)
		s.dispatch()
	}
}

func (s *naiveScheduler) expire(c *naiveTask) {
	c.prio = Prio(c.sp, c.s)
	c.tsLeft = Timeslice(c.sp)
	nr := len(s.tasks) - s.sleeping
	starving := naiveLen(&s.expired) > 0 && s.now-s.expiredTs >= StarveUnit*nr
	c.state = Queued
	if Interactive(c.s) && !starving {
		s.active[c.prio-BasePrio] = append(s.active[c.prio-BasePrio], c.id)
	} else {
		if naiveLen(&s.expired) == 0 {
			s.expiredTs = s.now
		}
		s.expired[c.prio-BasePrio] = append(s.expired[c.prio-BasePrio], c.id)
	}
	s.hasCur = false
	s.dispatch()
}

func (s *naiveScheduler) spawn(id, nice int) error {
	if id < 0 || nice < -20 || nice > 19 {
		return ErrInvalidArgs
	}
	if _, ok := s.tasks[id]; ok {
		return ErrDuplicateID
	}
	if len(s.tasks) >= s.n {
		return ErrFull
	}
	t := &naiveTask{id: id, nice: nice, sp: 120 + nice}
	t.tsLeft = Timeslice(t.sp)
	t.prio = Prio(t.sp, 0)
	s.tasks[id] = t
	s.arrive(t)
	return nil
}

func (s *naiveScheduler) tick() {
	s.now++
	if !s.hasCur {
		return
	}
	c := s.tasks[s.cur]
	c.tsLeft--
	if c.s > 0 {
		c.s--
	}
	if c.tsLeft > 0 {
		return
	}
	s.expire(c)
}

func (s *naiveScheduler) sleep() error {
	if !s.hasCur {
		return ErrNoCurrent
	}
	c := s.tasks[s.cur]
	c.state = Sleeping
	c.sleepStart = s.now
	s.sleeping++
	s.hasCur = false
	s.dispatch()
	return nil
}

func (s *naiveScheduler) wake(id int) error {
	t, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	if t.state != Sleeping {
		return ErrNotSleeping
	}
	t.s += s.now - t.sleepStart
	if t.s > MaxSleep {
		t.s = MaxSleep
	}
	t.prio = Prio(t.sp, t.s)
	s.sleeping--
	s.arrive(t)
	return nil
}

func (s *naiveScheduler) fork(id, child int) error {
	if id < 0 || child < 0 {
		return ErrInvalidArgs
	}
	if !s.hasCur {
		return ErrNoCurrent
	}
	p := s.tasks[s.cur]
	if p.id != id {
		return ErrNotCurrent
	}
	if _, ok := s.tasks[child]; ok {
		return ErrDuplicateID
	}
	if len(s.tasks) >= s.n {
		return ErrFull
	}
	t := p.tsLeft
	c := &naiveTask{id: child, nice: p.nice, sp: p.sp, s: p.s / 2, state: Queued}
	c.prio = Prio(c.sp, c.s)
	c.tsLeft = (t + 1) / 2
	p.tsLeft = t / 2
	if p.tsLeft == 0 {
		s.expire(p)
	}
	s.tasks[child] = c
	s.arrive(c)
	return nil
}

func (s *naiveScheduler) state(id int) (TaskInfo, bool) {
	t, ok := s.tasks[id]
	if !ok {
		return TaskInfo{}, false
	}
	return TaskInfo{State: t.state, Prio: t.prio, TsLeft: t.tsLeft, S: t.s}, true
}

func (s *naiveScheduler) current() (int, bool) {
	if !s.hasCur {
		return 0, false
	}
	return s.cur, true
}

func (s *naiveScheduler) queues() QueueSnapshot {
	return QueueSnapshot{
		Active:  naiveSnapshot(&s.active),
		Expired: naiveSnapshot(&s.expired),
	}
}

func naiveSnapshot(a *[NumPrios][]int) []PriorityQueue {
	var out []PriorityQueue
	for i := 0; i < NumPrios; i++ {
		if len(a[i]) == 0 {
			continue
		}
		ids := make([]int, len(a[i]))
		copy(ids, a[i])
		out = append(out, PriorityQueue{Prio: BasePrio + i, IDs: ids})
	}
	return out
}
