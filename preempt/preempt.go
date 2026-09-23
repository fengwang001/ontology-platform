package preempt

import (
	"container/heap"
	"errors"
)

var (
	ErrNotPreempted    = errors.New("preempt: task was never preempted")
	ErrAlreadyAcked    = errors.New("preempt: duplicate ack")
	ErrLateAck         = errors.New("preempt: ack arrived after timeout")
	ErrAlreadyNotified = errors.New("preempt: task already notified")
)

// Entry is one outstanding notification.
type Entry struct {
	TaskID   string
	Size     int64
	Deadline int64
	Owner    string // pending request that must receive the released capacity
}

type item struct {
	Entry
	seq     int64
	state   int
	heapIdx int
}

// Table is the two-phase preemption table. All methods are safe under a
// single external lock (quota.Allocator serializes everything).
type Table struct {
	byID   map[string]*item
	dl     deadlineHeap
	seqGen int64

	// NodesExamined is the number of heap items inspected by the last Tick.
	NodesExamined int
}

func New() *Table {
	t := &Table{byID: map[string]*item{}}
	heap.Init(&t.dl)
	return t
}

// Status: 1 = notified (in flight), 2 = acked, 3 = timed out, 0 = unknown.
func (t *Table) Status(taskID string) int {
	it, ok := t.byID[taskID]
	if !ok {
		return 0
	}
	return it.state
}

func (t *Table) Get(taskID string) (Entry, bool) {
	it, ok := t.byID[taskID]
	if !ok {
		return Entry{}, false
	}
	return it.Entry, true
}

// Notify opens an in-flight entry. Timeout fires exactly at Deadline
// (left-closed: now == Deadline forces reclaim on the next Tick).
func (t *Table) Notify(e Entry) error {
	if _, ok := t.byID[e.TaskID]; ok {
		return ErrAlreadyNotified
	}
	t.seqGen++
	it := &item{Entry: e, seq: t.seqGen}
	it.state = 1
	t.byID[e.TaskID] = it
	heap.Push(&t.dl, it)
	return nil
}

// Ack confirms victim exit. Returns ErrAlreadyAcked / ErrLateAck /
// ErrNotPreempted with zero side effects.
func (t *Table) Ack(taskID string, now int64) (Entry, error) {
	it, ok := t.byID[taskID]
	if !ok {
		return Entry{}, ErrNotPreempted
	}
	switch it.state {
	case 2:
		return Entry{}, ErrAlreadyAcked
	case 3:
		return Entry{}, ErrLateAck
	}
	if now >= it.Deadline {
		it.state = 3
		return Entry{}, ErrLateAck
	}
	it.state = 2
	return it.Entry, nil
}

// Remove drops a terminal/unknown entry (capacity released by that terminal
// event). Missing entry is a no-op-ok.
func (t *Table) Remove(taskID string) (Entry, bool) {
	it, ok := t.byID[taskID]
	if !ok {
		return Entry{}, false
	}
	if it.heapIdx >= 0 {
		heap.Remove(&t.dl, it.heapIdx)
	}
	delete(t.byID, taskID)
	return it.Entry, true
}

// Cancel is used when the victim Finishes concurrently: the notification is
// withdrawn and the capacity release is attributed to Owner.
func (t *Table) Cancel(taskID string) (Entry, bool) { return t.Remove(taskID) }

// Tick force-reaps every entry with Deadline <= now. Expired entries stay in
// the table in state 3 until quota removes them; they leave the heap at once.
func (t *Table) Tick(now int64) []Entry {
	t.NodesExamined = 0
	var out []Entry
	for t.dl.Len() > 0 {
		t.NodesExamined++
		top := t.dl[0]
		if top.Deadline > now {
			break
		}
		top.state = 3
		top.heapIdx = -1
		heap.Pop(&t.dl)
		out = append(out, top.Entry)
	}
	return out
}

// InFlight is the total size of stage-1 (unreleased) entries.
func (t *Table) InFlight() int64 {
	var s int64
	for _, it := range t.byID {
		if it.state == 1 {
			s += it.Size
		}
	}
	return s
}

func (t *Table) PendingFor(owner string) []Entry {
	var out []Entry
	for _, it := range t.byID {
		if it.state == 1 && it.Owner == owner {
			out = append(out, it.Entry)
		}
	}
	return out
}

type deadlineHeap []*item

func (h deadlineHeap) Len() int { return len(h) }
func (h deadlineHeap) Less(i, j int) bool {
	if h[i].Deadline != h[j].Deadline {
		return h[i].Deadline < h[j].Deadline
	}
	return h[i].seq < h[j].seq
}
func (h deadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}
func (h *deadlineHeap) Push(x any) {
	it := x.(*item)
	it.heapIdx = len(*h)
	*h = append(*h, it)
}
func (h *deadlineHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	it.heapIdx = -1
	*h = old[:n-1]
	return it
}
