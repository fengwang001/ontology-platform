package numa

import (
	"container/list"
	"errors"
	"sort"
	"sync"
)

var ErrInvalidConfig = errors.New("invalid numa team rwmutex configuration")

const (
	PhaseIdle = iota
	PhaseRead
	PhaseWrite
)

const (
	ThreadIdle = iota
	ThreadReadWaiting
	ThreadReadHolding
	ThreadWriteWaiting
	ThreadWriteHolding
)

const (
	ReasonThreadOutOfRange = "thread out of range"
	ReasonInvalidState     = "invalid thread state"
	ReasonUpgradeConflict  = "upgrade conflict"
)

type Phase uint8
type ThreadState uint8

type Config struct {
	Nodes      int
	Threads    int
	NodeOf     []int
	LocalLimit int
}

type Outcome struct {
	OK      bool
	Reason  string
	Granted []int
}

type Snapshot struct {
	Phase        Phase
	Readers      []int
	Writer       int
	WriteNode    int
	Passes       int
	ReadQueue    []int
	WriteQueues  [][]int
	GroupQueue   []int
	ThreadStates []ThreadState
}

type TeamRWMutex struct {
	mu sync.Mutex

	nodes      int
	threads    int
	nodeOf     []int
	localLimit int

	phase         Phase
	readers       map[int]struct{}
	writer        int
	writeNode     int
	passes        int
	readQueue     *list.List
	readElems     map[int]*list.Element
	writeQueue    []*list.List
	writeElems    map[int]*list.Element
	writerWaiters int
	group         *list.List
	groupElems    map[int]*list.Element
	state         []ThreadState
}

func New(cfg Config) (*TeamRWMutex, error) {
	if cfg.Nodes < 1 || cfg.Nodes > 8 {
		return nil, ErrInvalidConfig
	}
	if cfg.Threads < 1 || cfg.Threads > 32 {
		return nil, ErrInvalidConfig
	}
	if cfg.LocalLimit < 1 || cfg.LocalLimit > 16 {
		return nil, ErrInvalidConfig
	}
	if len(cfg.NodeOf) != cfg.Threads {
		return nil, ErrInvalidConfig
	}

	lock := &TeamRWMutex{
		nodes:      cfg.Nodes,
		threads:    cfg.Threads,
		nodeOf:     append([]int(nil), cfg.NodeOf...),
		localLimit: cfg.LocalLimit,
		state:      make([]ThreadState, cfg.Threads),
		readers:    make(map[int]struct{}, cfg.Threads),
		readQueue:  list.New(),
		readElems:  make(map[int]*list.Element, cfg.Threads),
		writeQueue: make([]*list.List, cfg.Nodes),
		writeElems: make(map[int]*list.Element, cfg.Threads),
		group:      list.New(),
		groupElems: make(map[int]*list.Element, cfg.Nodes),
		writer:     -1,
		writeNode:  -1,
	}
	for node := range lock.writeQueue {
		lock.writeQueue[node] = list.New()
	}
	for _, node := range lock.nodeOf {
		if node < 0 || node >= cfg.Nodes {
			return nil, ErrInvalidConfig
		}
	}
	return lock, nil
}

func (l *TeamRWMutex) RLock(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadIdle); !outcome.OK {
		return outcome
	}
	if l.phase == PhaseIdle || (l.phase == PhaseRead && l.writerWaiters == 0) {
		l.readers[thread] = struct{}{}
		l.state[thread] = ThreadReadHolding
		l.phase = PhaseRead
		l.writer = -1
		l.writeNode = -1
		l.passes = 0
		return Outcome{OK: true, Granted: []int{thread}}
	}

	l.readElems[thread] = l.readQueue.PushBack(thread)
	l.state[thread] = ThreadReadWaiting
	return Outcome{OK: true, Granted: []int{}}
}

func (l *TeamRWMutex) RUnlock(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadReadHolding); !outcome.OK {
		return outcome
	}
	delete(l.readers, thread)
	l.state[thread] = ThreadIdle
	granted := []int{}
	if len(l.readers) == 0 {
		granted = l.startWrite()
	}
	return Outcome{OK: true, Granted: granted}
}

func (l *TeamRWMutex) WLock(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadIdle); !outcome.OK {
		return outcome
	}
	if l.phase == PhaseIdle {
		l.phase = PhaseWrite
		l.writer = thread
		l.writeNode = l.nodeOf[thread]
		l.passes = 0
		l.state[thread] = ThreadWriteHolding
		return Outcome{OK: true, Granted: []int{thread}}
	}

	node := l.nodeOf[thread]
	l.writeElems[thread] = l.writeQueue[node].PushBack(thread)
	l.state[thread] = ThreadWriteWaiting
	l.writerWaiters++
	l.addNodeToGroup(node)
	return Outcome{OK: true, Granted: []int{}}
}

func (l *TeamRWMutex) WUnlock(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadWriteHolding); !outcome.OK {
		return outcome
	}

	gown := l.writeNode
	l.state[thread] = ThreadIdle
	l.writer = -1
	l.writeNode = -1

	if l.readQueue.Len() > 0 {
		granted := l.grantReadQueue()
		l.passes = 0
		if l.writeQueue[gown].Len() > 0 {
			l.addNodeToGroup(gown)
		}
		return Outcome{OK: true, Granted: granted}
	}

	if l.writeQueue[gown].Len() > 0 && (l.group.Len() == 0 || l.passes < l.localLimit) {
		next := l.popWriteWaiter(gown)
		l.writer = next
		l.writeNode = gown
		l.passes++
		l.state[next] = ThreadWriteHolding
		return Outcome{OK: true, Granted: []int{next}}
	}

	if l.writeQueue[gown].Len() > 0 {
		l.addNodeToGroup(gown)
	}
	granted := l.startWrite()
	if len(granted) == 0 {
		l.passes = 0
	}
	return Outcome{OK: true, Granted: granted}
}

func (l *TeamRWMutex) Downgrade(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadWriteHolding); !outcome.OK {
		return outcome
	}

	gown := l.writeNode
	l.writer = -1
	l.writeNode = -1
	l.passes = 0
	l.phase = PhaseRead
	l.readers[thread] = struct{}{}
	l.state[thread] = ThreadReadHolding
	granted := []int{thread}
	granted = append(granted, l.drainReadQueue()...)
	if l.writeQueue[gown].Len() > 0 {
		l.addNodeToGroup(gown)
	}
	return Outcome{OK: true, Granted: granted}
}

func (l *TeamRWMutex) Upgrade(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadReadHolding); !outcome.OK {
		return outcome
	}
	if len(l.readers) != 1 {
		return Outcome{OK: false, Reason: ReasonUpgradeConflict}
	}

	node := l.nodeOf[thread]
	delete(l.readers, thread)
	l.phase = PhaseWrite
	l.writer = thread
	l.writeNode = node
	l.passes = 0
	l.state[thread] = ThreadWriteHolding
	if elem := l.groupElems[node]; elem != nil {
		l.group.Remove(elem)
		delete(l.groupElems, node)
	}
	return Outcome{OK: true, Granted: []int{thread}}
}

func (l *TeamRWMutex) Cancel(thread int) Outcome {
	l.mu.Lock()
	defer l.mu.Unlock()

	if outcome := l.reject(thread, ThreadReadWaiting, ThreadWriteWaiting); !outcome.OK {
		return outcome
	}

	granted := []int{}
	if l.state[thread] == ThreadReadWaiting {
		l.readQueue.Remove(l.readElems[thread])
		delete(l.readElems, thread)
	} else {
		node := l.nodeOf[thread]
		l.writeQueue[node].Remove(l.writeElems[thread])
		delete(l.writeElems, thread)
		l.writerWaiters--
		if l.writeQueue[node].Len() == 0 {
			if elem := l.groupElems[node]; elem != nil {
				l.group.Remove(elem)
				delete(l.groupElems, node)
			}
		}
		if l.phase == PhaseRead && l.writerWaiters == 0 && l.readQueue.Len() > 0 {
			granted = l.grantReadQueue()
		}
	}
	l.state[thread] = ThreadIdle
	return Outcome{OK: true, Granted: granted}
}

func (l *TeamRWMutex) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	readers := make([]int, 0, len(l.readers))
	for reader := range l.readers {
		readers = append(readers, reader)
	}
	sort.Ints(readers)

	writeQueues := make([][]int, l.nodes)
	for node := range writeQueues {
		writeQueues[node] = queueValues(l.writeQueue[node])
	}

	return Snapshot{
		Phase:        l.phase,
		Readers:      readers,
		Writer:       l.writer,
		WriteNode:    l.writeNode,
		Passes:       l.passes,
		ReadQueue:    queueValues(l.readQueue),
		WriteQueues:  writeQueues,
		GroupQueue:   queueValues(l.group),
		ThreadStates: append([]ThreadState(nil), l.state...),
	}
}

func (l *TeamRWMutex) reject(thread int, allowed ...ThreadState) Outcome {
	if thread < 0 || thread >= l.threads {
		return Outcome{OK: false, Reason: ReasonThreadOutOfRange}
	}
	for _, allowedState := range allowed {
		if l.state[thread] == allowedState {
			return Outcome{OK: true}
		}
	}
	return Outcome{OK: false, Reason: ReasonInvalidState}
}

func (l *TeamRWMutex) addNodeToGroup(node int) {
	if l.phase == PhaseWrite && node == l.writeNode {
		return
	}
	if l.groupElems[node] != nil {
		return
	}
	l.groupElems[node] = l.group.PushBack(node)
}

func (l *TeamRWMutex) popWriteWaiter(node int) int {
	elem := l.writeQueue[node].Front()
	thread := l.writeQueue[node].Remove(elem).(int)
	delete(l.writeElems, thread)
	l.writerWaiters--
	return thread
}

func (l *TeamRWMutex) startWrite() []int {
	if l.group.Len() == 0 {
		l.phase = PhaseIdle
		l.writer = -1
		l.writeNode = -1
		l.passes = 0
		return []int{}
	}

	elem := l.group.Front()
	node := l.group.Remove(elem).(int)
	delete(l.groupElems, node)
	thread := l.popWriteWaiter(node)
	l.phase = PhaseWrite
	l.writer = thread
	l.writeNode = node
	l.passes = 0
	l.state[thread] = ThreadWriteHolding
	return []int{thread}
}

func (l *TeamRWMutex) grantReadQueue() []int {
	l.phase = PhaseRead
	return l.drainReadQueue()
}

func (l *TeamRWMutex) drainReadQueue() []int {
	granted := make([]int, 0, l.readQueue.Len())
	for l.readQueue.Len() > 0 {
		elem := l.readQueue.Front()
		thread := l.readQueue.Remove(elem).(int)
		delete(l.readElems, thread)
		l.readers[thread] = struct{}{}
		l.state[thread] = ThreadReadHolding
		granted = append(granted, thread)
	}
	return granted
}

func queueValues(queue *list.List) []int {
	values := make([]int, 0, queue.Len())
	for elem := queue.Front(); elem != nil; elem = elem.Next() {
		values = append(values, elem.Value.(int))
	}
	return values
}
