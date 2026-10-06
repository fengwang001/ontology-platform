package ontology

import (
	"container/heap"
	"container/list"
)

type entryState int

const (
	stateActive entryState = iota
	stateCanceled
	stateDone
)

const sourceCount = 5

type task struct {
	handle     Handle
	source     Source
	enqAt      Time
	seq        uint64
	depth      int
	idle       *idleCallback
	idleHandle Handle
	fn         TaskFn
}

type microtask struct {
	handle Handle
	depth  int
	fn     MicrotaskFn
}

type frameCallback struct {
	handle    Handle
	bornEpoch uint64
	fn        FrameFn
}

type idleCallback struct {
	handle    Handle
	timeout   Time
	dueAt     Time
	hasTO     bool
	bornEpoch uint64
	fn        IdleFn
}

type timerEntry struct {
	handle  Handle
	due     Time
	seq     uint64
	depth   int
	created bool
	fn      TaskFn
}

type sourceQueue struct {
	tasks  *list.List
	skips  int
	active bool
}

func newSourceQueue() sourceQueue {
	return sourceQueue{tasks: list.New()}
}

func (loop *Loop) allocLocked() Handle {
	loop.nextHandle++
	handle := loop.nextHandle
	loop.state[handle] = stateActive
	return handle
}

func (loop *Loop) nextSequenceLocked() uint64 {
	loop.nextSeq++
	return loop.nextSeq
}

func (loop *Loop) pushTaskLocked(item *task) {
	queue := loop.queues[item.source].tasks
	loop.queues[item.source].active = true
	if item.source == SourceTimer {
		element := queue.PushBack(item)
		loop.taskByID[item.handle] = element
		return
	}
	element := queue.PushBack(item)
	loop.taskByID[item.handle] = element
}

func (loop *Loop) pushTimerHeapLocked(item *timerEntry) {
	heap.Push(&loop.timers, item)
	loop.timerByID[item.handle] = item
}

type timerHeap []*timerEntry

func (heap timerHeap) Len() int {
	return len(heap)
}

func (heap timerHeap) Less(i, j int) bool {
	if heap[i].due != heap[j].due {
		return heap[i].due < heap[j].due
	}
	return heap[i].seq < heap[j].seq
}

func (heap timerHeap) Swap(i, j int) {
	heap[i], heap[j] = heap[j], heap[i]
}

func (heap *timerHeap) Push(value any) {
	*heap = append(*heap, value.(*timerEntry))
}

func (heap *timerHeap) Pop() any {
	old := *heap
	value := old[len(old)-1]
	old[len(old)-1] = nil
	*heap = old[:len(old)-1]
	return value
}

func validSource(source Source) bool {
	return source >= SourceUser && source <= SourceInternal
}

func (loop *Loop) cleanHead(source Source) {
	queue := &loop.queues[source]
	for queue.tasks.Front() != nil {
		element := queue.tasks.Front()
		head := element.Value.(*task)
		if loop.state[head.handle] != stateCanceled || loop.taskByID[head.handle] != element {
			return
		}
		queue.tasks.Remove(element)
		delete(loop.taskByID, head.handle)
	}
	queue.active = false
	queue.skips = 0
}

func (loop *Loop) selectTaskLocked() *task {
	var force *task
	forceSource := SourceInternal
	for source := SourceUser; source < sourceCount; source++ {
		loop.cleanHead(source)
		queue := &loop.queues[source]
		if queue.tasks.Len() == 0 {
			continue
		}
		head := queue.tasks.Front().Value.(*task)
		if head.enqAt > loop.now {
			continue
		}
		if queue.skips >= loop.config.StarvationLimit && (force == nil || head.enqAt < force.enqAt || head.enqAt == force.enqAt && source < forceSource) {
			force = head
			forceSource = source
		}
	}
	if force != nil {
		return force
	}
	for source := SourceUser; source < sourceCount; source++ {
		queue := &loop.queues[source]
		if queue.tasks.Len() > 0 && queue.tasks.Front().Value.(*task).enqAt <= loop.now {
			return queue.tasks.Front().Value.(*task)
		}
	}
	return nil
}

func (loop *Loop) recordSkipsLocked(chosen *task) {
	for source := SourceUser; source < sourceCount; source++ {
		queue := &loop.queues[source]
		loop.cleanHead(source)
		if queue.tasks.Len() == 0 {
			queue.skips = 0
			continue
		}
		head := queue.tasks.Front().Value.(*task)
		if head.enqAt > loop.now {
			continue
		}
		if head == chosen {
			queue.skips = 0
		} else if queue.skips < loop.config.StarvationLimit {
			queue.skips++
		}
	}
}
