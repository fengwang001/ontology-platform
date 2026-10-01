package scheduler

import "container/heap"

type podState uint8

const (
	stateActive podState = iota + 1
	stateBackoff
	stateUnschedulable
	stateInFlight
)

type pod struct {
	id        string
	priority  int
	addedAt   int64
	attempts  int64
	failBits  uint8
	expiresAt int64
	parkedAt  int64
	popSeq    int64
	state     podState

	activeIndex  int
	backoffIndex int
	parkedIndex  int
}

type activeQueue []*pod

func (q activeQueue) Len() int { return len(q) }
func (q activeQueue) Less(i, j int) bool {
	left, right := q[i], q[j]
	if left.priority != right.priority {
		return left.priority > right.priority
	}
	if left.addedAt != right.addedAt {
		return left.addedAt < right.addedAt
	}
	return left.id < right.id
}
func (q activeQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].activeIndex = i
	q[j].activeIndex = j
}
func (q *activeQueue) Push(value any) {
	entry := value.(*pod)
	entry.activeIndex = len(*q)
	*q = append(*q, entry)
}
func (q *activeQueue) Pop() any {
	old := *q
	last := len(old) - 1
	entry := old[last]
	entry.activeIndex = -1
	*q = old[:last]
	return entry
}

type backoffQueue []*pod

func (q backoffQueue) Len() int { return len(q) }
func (q backoffQueue) Less(i, j int) bool {
	left, right := q[i], q[j]
	if left.expiresAt != right.expiresAt {
		return left.expiresAt < right.expiresAt
	}
	return left.id < right.id
}
func (q backoffQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].backoffIndex = i
	q[j].backoffIndex = j
}
func (q *backoffQueue) Push(value any) {
	entry := value.(*pod)
	entry.backoffIndex = len(*q)
	*q = append(*q, entry)
}
func (q *backoffQueue) Pop() any {
	old := *q
	last := len(old) - 1
	entry := old[last]
	entry.backoffIndex = -1
	*q = old[:last]
	return entry
}

type unschedulableQueue []*pod

func (q unschedulableQueue) Len() int { return len(q) }
func (q unschedulableQueue) Less(i, j int) bool {
	left, right := q[i], q[j]
	if left.parkedAt != right.parkedAt {
		return left.parkedAt < right.parkedAt
	}
	return left.id < right.id
}
func (q unschedulableQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].parkedIndex = i
	q[j].parkedIndex = j
}
func (q *unschedulableQueue) Push(value any) {
	entry := value.(*pod)
	entry.parkedIndex = len(*q)
	*q = append(*q, entry)
}
func (q *unschedulableQueue) Pop() any {
	old := *q
	last := len(old) - 1
	entry := old[last]
	entry.parkedIndex = -1
	*q = old[:last]
	return entry
}

var _ heap.Interface = (*activeQueue)(nil)
var _ heap.Interface = (*backoffQueue)(nil)
var _ heap.Interface = (*unschedulableQueue)(nil)
