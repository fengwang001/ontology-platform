package scheduler

import (
	"container/heap"
	"math"
	"sync"
)

type Outcome uint8

const (
	OutcomeScheduled Outcome = iota + 1
	OutcomeFailed
)

type Pod struct {
	ID       string
	Priority int
	AddedAt  int64
	Attempts int64
}

type Sizes struct {
	Active        int
	Backoff       int
	Unschedulable int
	InFlight      int
}

type SchedulingQueue struct {
	mu                     sync.Mutex
	baseBackoff            int64
	maxBackoff             int64
	unschedulableRetention int64
	maxNow                 int64
	eventSeq               int64
	eventMasks             []uint8
	pods                   map[string]*pod
	active                 activeQueue
	backoff                backoffQueue
	unschedulable          unschedulableQueue
}

func NewSchedulingQueue(baseBackoff, maxBackoff, unschedulableRetention int64) (*SchedulingQueue, error) {
	if baseBackoff < 1 || baseBackoff > 1_000_000_000_000 ||
		maxBackoff < baseBackoff || maxBackoff > 1_000_000_000_000 ||
		unschedulableRetention < 1 || unschedulableRetention > 1_000_000_000_000 {
		return nil, ErrInvalidConfig
	}
	return &SchedulingQueue{
		baseBackoff:            baseBackoff,
		maxBackoff:             maxBackoff,
		unschedulableRetention: unschedulableRetention,
		pods:                   make(map[string]*pod),
	}, nil
}

func (q *SchedulingQueue) Add(id string, priority int, now int64) (err error) {
	if id == "" || now < 0 {
		return ErrInvalidArgument
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if now < q.maxNow {
		return ErrClockRollback
	}
	if _, exists := q.pods[id]; exists {
		return ErrPodAlreadyExists
	}

	entry := &pod{
		id:           id,
		priority:     priority,
		addedAt:      now,
		activeIndex:  -1,
		backoffIndex: -1,
		parkedIndex:  -1,
		state:        stateActive,
	}
	q.pods[id] = entry
	heap.Push(&q.active, entry)
	q.maxNow = now
	return nil
}

func (q *SchedulingQueue) Pop(now int64) (*Pod, bool, error) {
	if now < 0 {
		return nil, false, ErrInvalidArgument
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if now < q.maxNow {
		return nil, false, ErrClockRollback
	}
	q.advanceLocked(now)
	q.maxNow = now

	if q.active.Len() == 0 {
		return nil, false, nil
	}

	entry := heap.Pop(&q.active).(*pod)
	entry.attempts++
	entry.popSeq = q.eventSeq
	entry.state = stateInFlight
	return entry.snapshot(), true, nil
}

func (q *SchedulingQueue) Done(id string, outcome Outcome, failureBits, now int64) error {
	if id == "" || now < 0 || (outcome != OutcomeScheduled && outcome != OutcomeFailed) ||
		failureBits < 0 || failureBits > 255 {
		return ErrInvalidArgument
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if now < q.maxNow {
		return ErrClockRollback
	}
	entry, exists := q.pods[id]
	if !exists {
		return ErrPodNotFound
	}
	if entry.state != stateInFlight {
		return ErrPodNotInFlight
	}

	q.maxNow = now
	if outcome == OutcomeScheduled {
		delete(q.pods, id)
		return nil
	}

	entry.failBits = uint8(failureBits)
	entry.expiresAt = q.backoffDeadlineLocked(entry, now)
	if q.hasRelatedEventLocked(entry) {
		entry.state = stateBackoff
		heap.Push(&q.backoff, entry)
	} else {
		entry.state = stateUnschedulable
		entry.parkedAt = now
		heap.Push(&q.unschedulable, entry)
	}
	return nil
}

func (q *SchedulingQueue) Event(eventMask, now int64) error {
	if eventMask < 1 || eventMask > 255 || now < 0 {
		return ErrInvalidArgument
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if now < q.maxNow {
		return ErrClockRollback
	}

	mask := uint8(eventMask)
	q.eventSeq++
	q.eventMasks = append(q.eventMasks, mask)

	parked := q.unschedulable
	q.unschedulable = make(unschedulableQueue, 0, parked.Len())
	for _, entry := range parked {
		entry.parkedIndex = -1
		if entry.failBits == 0 || entry.failBits&mask != 0 {
			q.routeAfterUnparkLocked(entry, now)
		} else {
			heap.Push(&q.unschedulable, entry)
		}
	}
	q.maxNow = now
	return nil
}

func (q *SchedulingQueue) Advance(now int64) error {
	if now < 0 {
		return ErrInvalidArgument
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if now < q.maxNow {
		return ErrClockRollback
	}
	q.advanceLocked(now)
	q.maxNow = now
	return nil
}

func (q *SchedulingQueue) Remove(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	entry, exists := q.pods[id]
	if !exists {
		return ErrPodNotFound
	}

	switch entry.state {
	case stateActive:
		heap.Remove(&q.active, entry.activeIndex)
	case stateBackoff:
		heap.Remove(&q.backoff, entry.backoffIndex)
	case stateUnschedulable:
		heap.Remove(&q.unschedulable, entry.parkedIndex)
	}
	delete(q.pods, id)
	return nil
}

func (q *SchedulingQueue) Sizes() Sizes {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Sizes{
		Active:        q.active.Len(),
		Backoff:       q.backoff.Len(),
		Unschedulable: q.unschedulable.Len(),
		InFlight:      len(q.pods) - q.active.Len() - q.backoff.Len() - q.unschedulable.Len(),
	}
}

func (q *SchedulingQueue) advanceLocked(now int64) {
	for q.backoff.Len() > 0 && q.backoff[0].expiresAt <= now {
		entry := heap.Pop(&q.backoff).(*pod)
		entry.state = stateActive
		heap.Push(&q.active, entry)
	}

	retentionThreshold := now - q.unschedulableRetention
	for q.unschedulable.Len() > 0 && q.unschedulable[0].parkedAt <= retentionThreshold {
		entry := heap.Pop(&q.unschedulable).(*pod)
		q.routeAfterUnparkLocked(entry, now)
	}
}

func (q *SchedulingQueue) routeAfterUnparkLocked(entry *pod, now int64) {
	if now < entry.expiresAt {
		entry.state = stateBackoff
		heap.Push(&q.backoff, entry)
	} else {
		entry.state = stateActive
		heap.Push(&q.active, entry)
	}
}

func (q *SchedulingQueue) hasRelatedEventLocked(entry *pod) bool {
	for index := entry.popSeq; index < q.eventSeq; index++ {
		eventMask := q.eventMasks[index]
		if entry.failBits == 0 || entry.failBits&eventMask != 0 {
			return true
		}
	}
	return false
}

func (q *SchedulingQueue) backoffDeadlineLocked(entry *pod, now int64) int64 {
	shift := uint(entry.attempts - 1)
	var duration int64
	if shift >= 63 || q.baseBackoff > q.maxBackoff>>shift {
		duration = q.maxBackoff
	} else {
		duration = q.baseBackoff << shift
		if duration > q.maxBackoff {
			duration = q.maxBackoff
		}
	}
	if now > math.MaxInt64-duration {
		return math.MaxInt64
	}
	return now + duration
}

func (entry *pod) snapshot() *Pod {
	return &Pod{
		ID:       entry.id,
		Priority: entry.priority,
		AddedAt:  entry.addedAt,
		Attempts: entry.attempts,
	}
}
