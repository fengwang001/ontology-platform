package scheduler

import (
	"errors"
	"math/bits"
	"sync"
)

const (
	minNice = -20
	maxNice = 19

	minPrio = 100
	maxPrio = 139

	maxSleep     = 1000
	starvationAt = 100
)

var (
	ErrInvalidArgument     = errors.New("scheduler: invalid argument")
	ErrTaskExists          = errors.New("scheduler: task already exists")
	ErrSchedulerFull       = errors.New("scheduler: task limit reached")
	ErrNoCurrentTask       = errors.New("scheduler: no current task")
	ErrTaskNotFound        = errors.New("scheduler: task not found")
	ErrTaskNotSleeping     = errors.New("scheduler: task is not sleeping")
	ErrCurrentTaskMismatch = errors.New("scheduler: id is not the current task")
)

type State string

const (
	StateQueued   State = "queued"
	StateRunning  State = "running"
	StateSleeping State = "sleeping"
)

type TaskState struct {
	ID     int
	Nice   int
	SP     int
	State  State
	Prio   int
	TsLeft int
	Sleep  int
}

type QueueSnapshot struct {
	Active  map[int][]int
	Expired map[int][]int
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
	prev       *task
	next       *task
}

type fifo struct {
	head *task
	tail *task
}

type queueGroup struct {
	queues       [maxPrio - minPrio + 1]fifo
	bitmap       [3]uint64
	count        int
	minPrioIndex int
}

type Scheduler struct {
	mu          sync.Mutex
	limit       int
	now         int
	tasks       map[int]*task
	totalTasks  int
	active      queueGroup
	expired     queueGroup
	cur         *task
	expiredTs   int
	words       uint64
	opWords     uint64
	lastExpired bool
}

func New(n int) (*Scheduler, error) {
	if n < 1 || n > 100_000 {
		return nil, ErrInvalidArgument
	}
	return &Scheduler{
		limit:   n,
		tasks:   make(map[int]*task),
		active:  queueGroup{minPrioIndex: -1},
		expired: queueGroup{minPrioIndex: -1},
	}, nil
}

func (s *Scheduler) Spawn(id int, nice int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginOperation()
	defer s.finishOperation()
	if id < 0 || nice < minNice || nice > maxNice {
		return ErrInvalidArgument
	}
	if _, exists := s.tasks[id]; exists {
		return ErrTaskExists
	}
	if s.totalTasks >= s.limit {
		return ErrSchedulerFull
	}

	sp := 120 + nice
	t := &task{
		id:     id,
		nice:   nice,
		sp:     sp,
		prio:   DynamicPrio(sp, 0),
		tsLeft: TimeSlice(sp),
		state:  StateQueued,
	}
	s.tasks[id] = t
	s.totalTasks++
	s.arrive(t)
	return nil
}

func (s *Scheduler) Tick() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginOperation()
	defer s.finishOperation()
	s.now++
	if s.cur == nil {
		return s.now
	}

	s.cur.tsLeft--
	if s.cur.s > 0 {
		s.cur.s--
	}
	if s.cur.tsLeft == 0 {
		s.expireCurrent()
	}
	return s.now
}

func (s *Scheduler) Sleep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginOperation()
	defer s.finishOperation()
	if s.cur == nil {
		return ErrNoCurrentTask
	}
	s.cur.sleepStart = s.now
	s.cur.state = StateSleeping
	s.cur = nil
	s.dispatch()
	return nil
}

func (s *Scheduler) Wake(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginOperation()
	defer s.finishOperation()
	t, exists := s.tasks[id]
	if !exists {
		return ErrTaskNotFound
	}
	if t.state != StateSleeping {
		return ErrTaskNotSleeping
	}

	t.s = min(maxSleep, t.s+(s.now-t.sleepStart))
	t.prio = DynamicPrio(t.sp, t.s)
	t.state = StateQueued
	s.arrive(t)
	return nil
}

func (s *Scheduler) Fork(id int, child int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginOperation()
	defer s.finishOperation()
	if id < 0 || child < 0 {
		return ErrInvalidArgument
	}
	if s.cur == nil {
		return ErrNoCurrentTask
	}
	if id != s.cur.id {
		return ErrCurrentTaskMismatch
	}
	if _, exists := s.tasks[child]; exists {
		return ErrTaskExists
	}
	if s.totalTasks >= s.limit {
		return ErrSchedulerFull
	}

	parent := s.cur
	parentTime := parent.tsLeft
	childTask := &task{
		id:     child,
		nice:   parent.nice,
		sp:     parent.sp,
		s:      parent.s / 2,
		tsLeft: (parentTime + 1) / 2,
		state:  StateQueued,
	}
	childTask.prio = DynamicPrio(childTask.sp, childTask.s)
	parent.tsLeft = parentTime / 2

	if parent.tsLeft == 0 {
		s.expireCurrent()
	}

	s.tasks[child] = childTask
	s.totalTasks++
	s.arrive(childTask)
	return nil
}

func (s *Scheduler) State(id int) (TaskState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, exists := s.tasks[id]
	if !exists {
		return TaskState{}, ErrTaskNotFound
	}
	return TaskState{
		ID:     t.id,
		Nice:   t.nice,
		SP:     t.sp,
		State:  t.state,
		Prio:   t.prio,
		TsLeft: t.tsLeft,
		Sleep:  t.s,
	}, nil
}

func (s *Scheduler) Current() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return 0, false
	}
	return s.cur.id, true
}

func (s *Scheduler) Queues() QueueSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return QueueSnapshot{
		Active:  s.active.snapshot(),
		Expired: s.expired.snapshot(),
	}
}

func (s *Scheduler) ExpiredTs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiredTs
}

func (s *Scheduler) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *Scheduler) WordChecks() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.words
}

func (s *Scheduler) ResetWordChecks() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = 0
}

func (s *Scheduler) OperationWordChecks() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opWords
}

func (s *Scheduler) beginOperation() {
	s.opWords = 0
}

func (s *Scheduler) finishOperation() {
	if s.opWords > 4 {
		panic("scheduler: checked more than four bitmap words in one operation")
	}
}

func TimeSlice(sp int) int {
	if sp < 120 {
		return (140 - sp) * 20
	}
	return max(5, (140-sp)*5)
}

func Bonus(sleepAmount int) int {
	return sleepAmount / 100
}

func DynamicPrio(sp int, sleepAmount int) int {
	return min(maxPrio, max(minPrio, sp-Bonus(sleepAmount)))
}

func (s *Scheduler) dispatch() {
	if s.active.count == 0 {
		if s.expired.count == 0 {
			s.cur = nil
			return
		}
		s.active, s.expired = s.expired, s.active
		s.expiredTs = 0
		s.refreshQueueCaches()
	}
	if s.active.minPrioIndex < 0 {
		s.rebuildActiveCache()
	}

	prio := minPrio + s.active.minPrioIndex
	s.cur = s.active.popFront(prio, func() {
		s.words++
		s.opWords++
	})
	s.cur.state = StateRunning
}

func (s *Scheduler) rebuildActiveCache() {
	s.active.minPrioIndex = -1
	if s.active.count == 0 {
		return
	}
	for word := 0; word < len(s.active.bitmap); word++ {
		s.words++
		s.opWords++
		if s.active.bitmap[word] == 0 {
			continue
		}
		s.active.minPrioIndex = word*64 + bits.TrailingZeros64(s.active.bitmap[word])
		return
	}
}

func (s *Scheduler) refreshQueueCaches() {
	rebuild := func(group *queueGroup) {
		group.minPrioIndex = -1
		if group.count == 0 {
			return
		}
		for word := 0; word < len(group.bitmap); word++ {
			if group.bitmap[word] == 0 {
				continue
			}
			group.minPrioIndex = word*64 + bits.TrailingZeros64(group.bitmap[word])
			return
		}
	}
	rebuild(&s.active)
	rebuild(&s.expired)
}

func (s *Scheduler) arrive(t *task) {
	t.state = StateQueued
	if s.cur == nil {
		s.active.pushBack(t)
		s.dispatch()
		return
	}

	if t.prio < s.cur.prio {
		current := s.cur
		current.state = StateQueued
		s.cur = nil
		s.active.pushFront(current)
		s.active.pushBack(t)
		s.dispatch()
		return
	}

	s.active.pushBack(t)
}

func (s *Scheduler) expireCurrent() {
	t := s.cur
	t.prio = DynamicPrio(t.sp, t.s)
	t.tsLeft = TimeSlice(t.sp)

	nr := 1 + s.active.count + s.expired.count
	starving := s.expired.count > 0 && s.now-s.expiredTs >= starvationAt*nr
	destination := &s.expired
	if Bonus(t.s) >= 7 && !starving {
		destination = &s.active
	}

	if destination == &s.expired && s.expired.count == 0 {
		s.expiredTs = s.now
	}
	t.state = StateQueued
	s.lastExpired = destination == &s.expired
	destination.pushBack(t)
	s.cur = nil
	s.dispatch()
}

func (g *queueGroup) pushBack(t *task) {
	t.prev = nil
	t.next = nil
	q := &g.queues[t.prio-minPrio]
	if q.tail != nil {
		q.tail.next = t
		t.prev = q.tail
	} else {
		q.head = t
	}
	q.tail = t
	g.setBit(t.prio)
	g.count++
	index := t.prio - minPrio
	if g.minPrioIndex < 0 || index < g.minPrioIndex {
		g.minPrioIndex = index
	}
}

func (g *queueGroup) pushFront(t *task) {
	t.prev = nil
	t.next = nil
	q := &g.queues[t.prio-minPrio]
	if q.head != nil {
		q.head.prev = t
		t.next = q.head
	} else {
		q.tail = t
	}
	q.head = t
	g.setBit(t.prio)
	g.count++
	index := t.prio - minPrio
	if g.minPrioIndex < 0 || index < g.minPrioIndex {
		g.minPrioIndex = index
	}
}

func (g *queueGroup) popFront(prio int, onScan func()) *task {
	q := &g.queues[prio-minPrio]
	t := q.head
	q.head = t.next
	if q.head != nil {
		q.head.prev = nil
		g.count--
	} else {
		q.tail = nil
		g.clearBit(prio)
		g.minPrioIndex = -1
		g.count--
		if g.count > 0 {
			g.findMinPrio(onScan)
		}
	}
	t.prev = nil
	t.next = nil
	return t
}

func (g *queueGroup) findMinPrio(onScan func()) {
	for word := 0; word < len(g.bitmap); word++ {
		onScan()
		if g.bitmap[word] == 0 {
			continue
		}
		g.minPrioIndex = word*64 + bits.TrailingZeros64(g.bitmap[word])
		return
	}
}

func (g *queueGroup) setBit(prio int) {
	index := prio - minPrio
	g.bitmap[index/64] |= 1 << (index % 64)
}

func (g *queueGroup) clearBit(prio int) {
	index := prio - minPrio
	g.bitmap[index/64] &^= 1 << (index % 64)
}

func (g *queueGroup) snapshot() map[int][]int {
	result := make(map[int][]int)
	for index := range g.queues {
		q := &g.queues[index]
		if q.head == nil {
			continue
		}
		ids := make([]int, 0)
		for t := q.head; t != nil; t = t.next {
			ids = append(ids, t.id)
		}
		result[minPrio+index] = ids
	}
	return result
}
