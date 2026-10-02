// Package scheduler implements an O(1)-style single-CPU scheduler with
// active/expired priority-bucket queues, sleep-derived dynamic priority
// bonuses, static-priority-dependent timeslices, wakeup preemption and
// expired-queue starvation protection.
package scheduler

import (
	"container/list"
	"errors"
	"math/bits"
	"sync"
)

const (
	// NumPrios is the number of dynamic priority buckets.
	NumPrios = 40
	// BasePrio is the numerically smallest (best) dynamic priority.
	BasePrio = 100
	// MaxSleep is the cap of the sleep counter s.
	MaxSleep = 1000
	// StarveUnit is the per-task tick budget before the expired array is
	// considered starving: starving iff now-expiredTs >= StarveUnit*nr.
	StarveUnit = 100
)

var (
	ErrInvalidArgs = errors.New("scheduler: invalid arguments")
	ErrDuplicateID = errors.New("scheduler: duplicate task id")
	ErrFull        = errors.New("scheduler: task limit reached")
	ErrNoCurrent   = errors.New("scheduler: no current task")
	ErrNotFound    = errors.New("scheduler: task not found")
	ErrNotSleeping = errors.New("scheduler: task not sleeping")
	ErrNotCurrent  = errors.New("scheduler: task is not the current task")
)

// State is the lifecycle state of a task.
type State int

const (
	// Queued means the task sits in the active or expired array.
	Queued State = iota
	// Running means the task is the current task.
	Running
	// Sleeping means the task waits for Wake and is in no queue.
	Sleeping
)

func (s State) String() string {
	switch s {
	case Queued:
		return "queued"
	case Running:
		return "running"
	case Sleeping:
		return "sleeping"
	}
	return "unknown"
}

// Timeslice returns the timeslice for a static priority sp in [100,139].
func Timeslice(sp int) int {
	if sp < 120 {
		return (140 - sp) * 20
	}
	if v := (140 - sp) * 5; v > 5 {
		return v
	}
	return 5
}

// Bonus returns the dynamic priority bonus derived from sleep counter s.
func Bonus(s int) int { return s / 100 }

// Prio computes the dynamic priority for static priority sp and sleep
// counter s, clamped to [BasePrio, BasePrio+NumPrios-1].
func Prio(sp, s int) int {
	p := sp - Bonus(s) + 5
	if p < BasePrio {
		return BasePrio
	}
	if p > BasePrio+NumPrios-1 {
		return BasePrio + NumPrios - 1
	}
	return p
}

// Interactive reports whether a task with sleep counter s is interactive.
func Interactive(s int) bool { return Bonus(s) >= 7 }

// TaskInfo is the observable state of a single task.
type TaskInfo struct {
	State  State
	Prio   int
	TsLeft int
	S      int
}

type task struct {
	id         int
	nice       int
	sp         int
	s          int
	prio       int
	tsLeft     int
	state      State
	sleepStart int
}

// bitmap is a two-level priority bitmap over NumPrios buckets. find
// examines at most one summary word plus one group word.
type bitmap struct {
	summary uint64
	words   [(NumPrios + 63) / 64]uint64
}

func (b *bitmap) set(idx int) {
	w := idx / 64
	b.words[w] |= 1 << (uint(idx) % 64)
	b.summary |= 1 << uint(w)
}

func (b *bitmap) clear(idx int) {
	w := idx / 64
	b.words[w] &^= 1 << (uint(idx) % 64)
	if b.words[w] == 0 {
		b.summary &^= 1 << uint(w)
	}
}

// find returns the lowest set bucket index, or -1 when empty. examined
// counts the bitmap words read.
func (b *bitmap) find(examined *int) int {
	*examined++
	if b.summary == 0 {
		return -1
	}
	w := bits.TrailingZeros64(b.summary)
	*examined++
	return w*64 + bits.TrailingZeros64(b.words[w])
}

// queueArray is one array of NumPrios FIFO queues plus its bitmap.
type queueArray struct {
	qs [NumPrios]list.List
	bm bitmap
	n  int
}

func (a *queueArray) pushBack(t *task) {
	idx := t.prio - BasePrio
	a.qs[idx].PushBack(t)
	a.bm.set(idx)
	a.n++
}

func (a *queueArray) pushFront(t *task) {
	idx := t.prio - BasePrio
	a.qs[idx].PushFront(t)
	a.bm.set(idx)
	a.n++
}

func (a *queueArray) popHead(idx int) *task {
	e := a.qs[idx].Front()
	t := e.Value.(*task)
	a.qs[idx].Remove(e)
	if a.qs[idx].Len() == 0 {
		a.bm.clear(idx)
	}
	a.n--
	return t
}

// PriorityQueue lists the task ids of one non-empty priority bucket,
// front (next to run) first.
type PriorityQueue struct {
	Prio int
	IDs  []int
}

// QueueSnapshot is a point-in-time copy of both queue arrays.
type QueueSnapshot struct {
	Active  []PriorityQueue
	Expired []PriorityQueue
}

// Scheduler is a single-CPU O(1)-style scheduler. All methods are safe
// for concurrent use; the result is equivalent to some serial order.
type Scheduler struct {
	mu        sync.Mutex
	n         int
	now       int
	tasks     map[int]*task
	sleeping  int
	active    *queueArray
	expired   *queueArray
	cur       *task
	expiredTs int
	words     int // bitmap words examined by the last mutating operation
}

// New creates a scheduler allowing at most n tasks (1 <= n <= 1e5).
func New(n int) *Scheduler {
	if n < 1 {
		panic("scheduler: n must be >= 1")
	}
	return &Scheduler{
		n:       n,
		tasks:   make(map[int]*task),
		active:  &queueArray{},
		expired: &queueArray{},
	}
}

// dispatchLocked selects the next task to run: the head of the
// lowest-prio non-empty active queue. When the active array is empty but
// the expired array is not, the two arrays are swapped and expiredTs is
// reset before selecting. When both are empty the CPU goes idle.
func (s *Scheduler) dispatchLocked() {
	if s.active.n == 0 {
		if s.expired.n == 0 {
			s.cur = nil
			return
		}
		s.active, s.expired = s.expired, s.active
		s.expiredTs = 0
	}
	idx := s.active.bm.find(&s.words)
	t := s.active.popHead(idx)
	t.state = Running
	s.cur = t
}

// arriveLocked appends t to the tail of its active queue, then dispatches
// if the CPU is idle, or preempts the current task when t has a strictly
// smaller prio (the preempted task returns to the head of its active
// queue keeping its remaining timeslice).
func (s *Scheduler) arriveLocked(t *task) {
	t.state = Queued
	s.active.pushBack(t)
	if s.cur == nil {
		s.dispatchLocked()
		return
	}
	if t.prio < s.cur.prio {
		c := s.cur
		s.cur = nil
		c.state = Queued
		s.active.pushFront(c)
		s.dispatchLocked()
	}
}

// expireLocked handles a timeslice expiry of c: recompute prio, reset the
// timeslice, decide starving before enqueueing, enqueue to the active
// array when interactive and not starving, otherwise to the expired
// array (setting expiredTs when it was empty), then dispatch. It does
// not advance the clock nor touch s.
func (s *Scheduler) expireLocked(c *task) {
	c.prio = Prio(c.sp, c.s)
	c.tsLeft = Timeslice(c.sp)
	nr := len(s.tasks) - s.sleeping
	starving := s.expired.n > 0 && s.now-s.expiredTs >= StarveUnit*nr
	c.state = Queued
	if Interactive(c.s) && !starving {
		s.active.pushBack(c)
	} else {
		if s.expired.n == 0 {
			s.expiredTs = s.now
		}
		s.expired.pushBack(c)
	}
	s.cur = nil
	s.dispatchLocked()
}

// Spawn creates a task and enqueues it.
func (s *Scheduler) Spawn(id, nice int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = 0
	if id < 0 || nice < -20 || nice > 19 {
		return ErrInvalidArgs
	}
	if _, ok := s.tasks[id]; ok {
		return ErrDuplicateID
	}
	if len(s.tasks) >= s.n {
		return ErrFull
	}
	t := &task{id: id, nice: nice, sp: 120 + nice}
	t.tsLeft = Timeslice(t.sp)
	t.prio = Prio(t.sp, 0)
	s.tasks[id] = t
	s.arriveLocked(t)
	return nil
}

// Tick advances the clock by one tick.
func (s *Scheduler) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = 0
	s.now++
	c := s.cur
	if c == nil {
		return
	}
	c.tsLeft--
	if c.s > 0 {
		c.s--
	}
	if c.tsLeft > 0 {
		return
	}
	s.expireLocked(c)
}

// Sleep puts the current task to sleep, keeping its prio and remaining
// timeslice, then dispatches.
func (s *Scheduler) Sleep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = 0
	c := s.cur
	if c == nil {
		return ErrNoCurrent
	}
	c.state = Sleeping
	c.sleepStart = s.now
	s.sleeping++
	s.cur = nil
	s.dispatchLocked()
	return nil
}

// Wake wakes a sleeping task: its sleep counter grows by the slept
// duration (capped at MaxSleep), prio is recomputed and the task is
// enqueued, keeping its remaining timeslice.
func (s *Scheduler) Wake(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = 0
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
	s.arriveLocked(t)
	return nil
}

// Fork lets the current task spawn a child, splitting its timeslice: the
// child gets ceil(t/2), the parent keeps floor(t/2). When the parent's
// share is zero (t == 1) the parent expires immediately (same flow as a
// timeslice expiry, without advancing the clock or changing s, and the
// not-yet-enqueued child is not counted in nr) before the child arrives.
func (s *Scheduler) Fork(id, child int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = 0
	if id < 0 || child < 0 {
		return ErrInvalidArgs
	}
	p := s.cur
	if p == nil {
		return ErrNoCurrent
	}
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
	c := &task{id: child, nice: p.nice, sp: p.sp, s: p.s / 2, state: Queued}
	c.prio = Prio(c.sp, c.s)
	c.tsLeft = (t + 1) / 2
	p.tsLeft = t / 2
	if p.tsLeft == 0 {
		s.expireLocked(p)
	}
	s.tasks[child] = c
	s.arriveLocked(c)
	return nil
}

// State returns the observable state of task id.
func (s *Scheduler) State(id int) (TaskInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return TaskInfo{}, false
	}
	return TaskInfo{State: t.state, Prio: t.prio, TsLeft: t.tsLeft, S: t.s}, true
}

// Current returns the id of the running task, if any.
func (s *Scheduler) Current() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return 0, false
	}
	return s.cur.id, true
}

// Now returns the current clock in ticks.
func (s *Scheduler) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Queues snapshots both queue arrays.
func (s *Scheduler) Queues() QueueSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return QueueSnapshot{Active: snapshotArray(s.active), Expired: snapshotArray(s.expired)}
}

func snapshotArray(a *queueArray) []PriorityQueue {
	var out []PriorityQueue
	for idx := 0; idx < NumPrios; idx++ {
		if a.qs[idx].Len() == 0 {
			continue
		}
		pq := PriorityQueue{Prio: BasePrio + idx}
		for e := a.qs[idx].Front(); e != nil; e = e.Next() {
			pq.IDs = append(pq.IDs, e.Value.(*task).id)
		}
		out = append(out, pq)
	}
	return out
}

// ExpiredTs returns the expired-array start timestamp (0 when empty).
func (s *Scheduler) ExpiredTs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiredTs
}
