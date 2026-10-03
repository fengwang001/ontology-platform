package numa

import (
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type randomCall struct {
	op     string
	thread int
}

type naiveLock struct {
	nodes      int
	threads    int
	localLimit int
	nodeOf     []int
	phase      Phase
	readers    map[int]struct{}
	writer     int
	writeNode  int
	passes     int
	readQueue  []int
	writeQueue [][]int
	groupQueue []int
	state      []ThreadState
}

func newNaive(cfg Config) *naiveLock {
	writeQueues := make([][]int, cfg.Nodes)
	return &naiveLock{
		nodes:      cfg.Nodes,
		threads:    cfg.Threads,
		localLimit: cfg.LocalLimit,
		nodeOf:     append([]int(nil), cfg.NodeOf...),
		readers:    make(map[int]struct{}),
		writer:     -1,
		writeNode:  -1,
		writeQueue: writeQueues,
		state:      make([]ThreadState, cfg.Threads),
	}
}

func dispatch(lock *TeamRWMutex, call randomCall) Outcome {
	switch call.op {
	case "RLock":
		return lock.RLock(call.thread)
	case "RUnlock":
		return lock.RUnlock(call.thread)
	case "WLock":
		return lock.WLock(call.thread)
	case "WUnlock":
		return lock.WUnlock(call.thread)
	case "Downgrade":
		return lock.Downgrade(call.thread)
	case "Upgrade":
		return lock.Upgrade(call.thread)
	case "Cancel":
		return lock.Cancel(call.thread)
	default:
		return Outcome{OK: false, Reason: ReasonInvalidState}
	}
}

func (m *naiveLock) apply(call randomCall) (Outcome, string) {
	if call.thread < 0 || call.thread >= m.threads {
		return Outcome{OK: false, Reason: ReasonThreadOutOfRange}, "thread id is outside [0,T)"
	}

	switch call.op {
	case "RLock", "WLock":
		if m.state[call.thread] != ThreadIdle {
			return Outcome{OK: false, Reason: ReasonInvalidState}, "operation requires idle thread"
		}
	case "RUnlock":
		if m.state[call.thread] != ThreadReadHolding {
			return Outcome{OK: false, Reason: ReasonInvalidState}, "RUnlock requires read holder"
		}
	case "WUnlock", "Downgrade":
		if m.state[call.thread] != ThreadWriteHolding {
			return Outcome{OK: false, Reason: ReasonInvalidState}, "operation requires write holder"
		}
	case "Upgrade":
		if m.state[call.thread] != ThreadReadHolding {
			return Outcome{OK: false, Reason: ReasonInvalidState}, "Upgrade requires read holder"
		}
		if len(m.readers) != 1 {
			return Outcome{OK: false, Reason: ReasonUpgradeConflict}, "R is not exactly the upgrading reader"
		}
	case "Cancel":
		if m.state[call.thread] != ThreadReadWaiting && m.state[call.thread] != ThreadWriteWaiting {
			return Outcome{OK: false, Reason: ReasonInvalidState}, "Cancel requires waiting thread"
		}
	default:
		return Outcome{OK: false, Reason: ReasonInvalidState}, "unknown operation"
	}

	switch call.op {
	case "RLock":
		return m.rlock(call.thread)
	case "RUnlock":
		return m.runlock(call.thread)
	case "WLock":
		return m.wlock(call.thread)
	case "WUnlock":
		return m.wunlock(call.thread)
	case "Downgrade":
		return m.downgrade(call.thread)
	case "Upgrade":
		return m.upgrade(call.thread)
	case "Cancel":
		return m.cancel(call.thread)
	default:
		return Outcome{OK: false, Reason: ReasonInvalidState}, "unknown operation"
	}
}

func (m *naiveLock) snapshot() Snapshot {
	readers := make([]int, 0, len(m.readers))
	for reader := range m.readers {
		readers = append(readers, reader)
	}
	sort.Ints(readers)
	return Snapshot{
		Phase:        m.phase,
		Readers:      readers,
		Writer:       m.writer,
		WriteNode:    m.writeNode,
		Passes:       m.passes,
		ReadQueue:    append([]int(nil), m.readQueue...),
		WriteQueues:  deepQueues(m.writeQueue),
		GroupQueue:   append([]int(nil), m.groupQueue...),
		ThreadStates: append([]ThreadState(nil), m.state...),
	}
}

func (m *naiveLock) rlock(thread int) (Outcome, string) {
	if m.phase == PhaseIdle || (m.phase == PhaseRead && !m.hasWriterWaiting()) {
		m.phase = PhaseRead
		m.readers[thread] = struct{}{}
		m.state[thread] = ThreadReadHolding
		m.writer = -1
		m.writeNode = -1
		m.passes = 0
		return Outcome{OK: true, Granted: []int{thread}}, "idle, or read phase with no writer waiting"
	}
	m.readQueue = append(m.readQueue, thread)
	m.state[thread] = ThreadReadWaiting
	return Outcome{OK: true, Granted: []int{}}, "reader joins global FIFO Qr"
}

func (m *naiveLock) runlock(thread int) (Outcome, string) {
	delete(m.readers, thread)
	m.state[thread] = ThreadIdle
	if len(m.readers) > 0 {
		return Outcome{OK: true, Granted: []int{}}, "other readers remain"
	}
	return Outcome{OK: true, Granted: m.startWrite()}, "last reader starts pending write group"
}

func (m *naiveLock) wlock(thread int) (Outcome, string) {
	if m.phase == PhaseIdle {
		m.phase = PhaseWrite
		m.writer = thread
		m.writeNode = m.nodeOf[thread]
		m.passes = 0
		m.state[thread] = ThreadWriteHolding
		return Outcome{OK: true, Granted: []int{thread}}, "idle phase grants writer"
	}
	node := m.nodeOf[thread]
	m.writeQueue[node] = append(m.writeQueue[node], thread)
	m.state[thread] = ThreadWriteWaiting
	m.addNode(node)
	return Outcome{OK: true, Granted: []int{}}, "writer joins node queue and possibly appends node to G"
}

func (m *naiveLock) wunlock(thread int) (Outcome, string) {
	gown := m.writeNode
	m.state[thread] = ThreadIdle
	m.writer = -1
	m.writeNode = -1

	if len(m.readQueue) > 0 {
		granted := append([]int(nil), m.readQueue...)
		m.readers = make(map[int]struct{}, len(granted))
		for _, reader := range granted {
			m.readers[reader] = struct{}{}
			m.state[reader] = ThreadReadHolding
		}
		m.readQueue = nil
		m.phase = PhaseRead
		m.passes = 0
		if len(m.writeQueue[gown]) > 0 {
			m.addNode(gown)
		}
		return Outcome{OK: true, Granted: granted}, "Qr is granted before local handoff"
	}

	if len(m.writeQueue[gown]) > 0 && (len(m.groupQueue) == 0 || m.passes < m.localLimit) {
		next := m.writeQueue[gown][0]
		m.writeQueue[gown] = m.writeQueue[gown][1:]
		m.writer = next
		m.writeNode = gown
		m.passes++
		m.state[next] = ThreadWriteHolding
		return Outcome{OK: true, Granted: []int{next}}, "local handoff because G is empty or p < B"
	}

	if len(m.writeQueue[gown]) > 0 {
		m.addNode(gown)
	}
	return Outcome{OK: true, Granted: m.startWrite()}, "local budget exhausted or another node leads"
}

func (m *naiveLock) downgrade(thread int) (Outcome, string) {
	gown := m.writeNode
	m.writer = -1
	m.writeNode = -1
	m.passes = 0
	m.phase = PhaseRead
	m.readers[thread] = struct{}{}
	m.state[thread] = ThreadReadHolding
	granted := []int{thread}
	granted = append(granted, m.readQueue...)
	for _, reader := range m.readQueue {
		m.readers[reader] = struct{}{}
		m.state[reader] = ThreadReadHolding
	}
	m.readQueue = nil
	if len(m.writeQueue[gown]) > 0 {
		m.addNode(gown)
	}
	return Outcome{OK: true, Granted: granted}, "downgraded writer first, then all Qr readers"
}

func (m *naiveLock) upgrade(thread int) (Outcome, string) {
	node := m.nodeOf[thread]
	delete(m.readers, thread)
	m.phase = PhaseWrite
	m.writer = thread
	m.writeNode = node
	m.passes = 0
	m.state[thread] = ThreadWriteHolding
	m.groupQueue = removeInt(m.groupQueue, node)
	return Outcome{OK: true, Granted: []int{thread}}, "sole reader upgrades and its node leaves G"
}

func (m *naiveLock) cancel(thread int) (Outcome, string) {
	if m.state[thread] == ThreadReadWaiting {
		m.readQueue = removeInt(m.readQueue, thread)
		m.state[thread] = ThreadIdle
		return Outcome{OK: true, Granted: []int{}}, "read waiter leaves Qr"
	}

	node := m.nodeOf[thread]
	m.writeQueue[node] = removeInt(m.writeQueue[node], thread)
	m.state[thread] = ThreadIdle
	if len(m.writeQueue[node]) == 0 {
		m.groupQueue = removeInt(m.groupQueue, node)
	}
	if m.phase == PhaseRead && !m.hasWriterWaiting() && len(m.readQueue) > 0 {
		granted := append([]int(nil), m.readQueue...)
		for _, reader := range granted {
			m.readers[reader] = struct{}{}
			m.state[reader] = ThreadReadHolding
		}
		m.readQueue = nil
		return Outcome{OK: true, Granted: granted}, "last writer cancellation releases queued readers"
	}
	return Outcome{OK: true, Granted: []int{}}, "write waiter leaves Qw and empty node leaves G"
}

func (m *naiveLock) startWrite() []int {
	if len(m.groupQueue) == 0 {
		m.phase = PhaseIdle
		m.writer = -1
		m.writeNode = -1
		m.passes = 0
		return []int{}
	}

	node := m.groupQueue[0]
	m.groupQueue = m.groupQueue[1:]
	thread := m.writeQueue[node][0]
	m.writeQueue[node] = m.writeQueue[node][1:]
	m.phase = PhaseWrite
	m.writer = thread
	m.writeNode = node
	m.passes = 0
	m.state[thread] = ThreadWriteHolding
	return []int{thread}
}

func (m *naiveLock) addNode(node int) {
	if m.phase == PhaseWrite && node == m.writeNode {
		return
	}
	for _, queuedNode := range m.groupQueue {
		if queuedNode == node {
			return
		}
	}
	m.groupQueue = append(m.groupQueue, node)
}

func (m *naiveLock) hasWriterWaiting() bool {
	for _, queue := range m.writeQueue {
		if len(queue) > 0 {
			return true
		}
	}
	return false
}

func removeInt(values []int, target int) []int {
	for index, value := range values {
		if value == target {
			return append(values[:index], values[index+1:]...)
		}
	}
	return values
}

func deepQueues(queues [][]int) [][]int {
	copied := make([][]int, len(queues))
	for node, queue := range queues {
		copied[node] = append([]int(nil), queue...)
	}
	return copied
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	if snapshot.Readers == nil {
		snapshot.Readers = []int{}
	}
	if snapshot.ReadQueue == nil {
		snapshot.ReadQueue = []int{}
	}
	if snapshot.GroupQueue == nil {
		snapshot.GroupQueue = []int{}
	}
	for node, queue := range snapshot.WriteQueues {
		if queue == nil {
			snapshot.WriteQueues[node] = []int{}
		}
	}
	return snapshot
}

func TestRandomSequencesMatchNaive(t *testing.T) {
	operations := []string{"RLock", "RUnlock", "WLock", "WUnlock", "Downgrade", "Upgrade", "Cancel"}
	for iteration := 0; iteration < 2000; iteration++ {
		random := rand.New(rand.NewSource(int64(9181279000 + iteration)))
		cfg := Config{
			Nodes:      random.Intn(8) + 1,
			Threads:    random.Intn(32) + 1,
			LocalLimit: random.Intn(16) + 1,
		}
		cfg.NodeOf = make([]int, cfg.Threads)
		for thread := range cfg.NodeOf {
			cfg.NodeOf[thread] = random.Intn(cfg.Nodes)
		}

		actual, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaive(cfg)
		callCount := random.Intn(25) + 12
		t.Logf("iteration=%d cfg={M:%d T:%d B:%d nodeOf:%v}", iteration, cfg.Nodes, cfg.Threads, cfg.LocalLimit, cfg.NodeOf)

		for index := 0; index < callCount; index++ {
			call := makeRandomCall(random, model.snapshot(), operations)
			outcome := dispatch(actual, call)
			expected, basis := model.apply(call)
			t.Logf("call %02d: input=%s(%d) output={ok:%t reason:%q granted:%v} basis=%s",
				index, call.op, call.thread, outcome.OK, outcome.Reason, outcome.Granted, basis)
			if !reflect.DeepEqual(normalizeOutcome(outcome), normalizeOutcome(expected)) {
				t.Fatalf("iteration %d call %d outcome mismatch\ngot=%+v\nwant=%+v", iteration, index, outcome, expected)
			}
			actualSnapshot := actual.Snapshot()
			expectedSnapshot := model.snapshot()
			assertInvariants(t, actualSnapshot)
			if !reflect.DeepEqual(normalizeSnapshot(actualSnapshot), normalizeSnapshot(expectedSnapshot)) {
				t.Fatalf("iteration %d call %d snapshot mismatch\ngot=%+v\nwant=%+v", iteration, index, actualSnapshot, expectedSnapshot)
			}
		}
	}
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	lock := newExampleLock(t)
	operations := []string{"RLock", "RUnlock", "WLock", "WUnlock", "Downgrade", "Upgrade", "Cancel"}
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			random := rand.New(rand.NewSource(int64(7000 + worker)))
			for index := 0; index < 120; index++ {
				call := makeRandomCall(random, lock.Snapshot(), operations)
				dispatch(lock, call)
			}
		}(worker)
	}
	wait.Wait()
	assertInvariants(t, lock.Snapshot())
}

func makeRandomCall(random *rand.Rand, snapshot Snapshot, operations []string) randomCall {
	threadCount := len(snapshot.ThreadStates)
	if random.Intn(100) < 5 {
		thread := threadCount
		if random.Intn(2) == 0 {
			thread = -1
		}
		return randomCall{op: operations[random.Intn(len(operations))], thread: thread}
	}

	preferred := preferredOperations(snapshot)
	if len(preferred) > 0 && random.Intn(100) < 82 {
		choice := preferred[0]
		thread := pickThread(random, snapshot.ThreadStates, choice.threadState)
		if choice.op == "WUnlock" || choice.op == "Downgrade" {
			thread = snapshot.Writer
		}
		return randomCall{op: choice.op, thread: thread}
	}

	thread := random.Intn(threadCount)
	return randomCall{op: operations[random.Intn(len(operations))], thread: thread}
}

type preferredChoice struct {
	op          string
	threadState ThreadState
}

func preferredOperations(snapshot Snapshot) []preferredChoice {
	waitingReaders := countState(snapshot.ThreadStates, ThreadReadWaiting)
	waitingWriters := countState(snapshot.ThreadStates, ThreadWriteWaiting)
	holdingReaders := countState(snapshot.ThreadStates, ThreadReadHolding)
	switch {
	case snapshot.Writer >= 0 && waitingReaders > 0:
		return []preferredChoice{{op: "WUnlock", threadState: ThreadWriteHolding}}
	case snapshot.Writer >= 0:
		if randomChoiceWriteRelease(snapshot) {
			return []preferredChoice{{op: "Downgrade", threadState: ThreadWriteHolding}}
		}
		return []preferredChoice{{op: "WUnlock", threadState: ThreadWriteHolding}}
	case waitingWriters > 0 && snapshot.Writer < 0:
		return []preferredChoice{{op: "Cancel", threadState: ThreadWriteWaiting}}
	case waitingReaders > 0 && snapshot.Writer < 0:
		if holdingReaders == 0 {
			return nil
		}
		return []preferredChoice{{op: "RUnlock", threadState: ThreadReadHolding}}
	case holdingReaders > 0 && snapshot.Writer < 0:
		return []preferredChoice{{op: "RUnlock", threadState: ThreadReadHolding}}
	default:
		return nil
	}
}

func randomChoiceWriteRelease(snapshot Snapshot) bool {
	return len(snapshot.Readers) == 0 && len(snapshot.ReadQueue) == 0
}

func countState(states []ThreadState, wanted ThreadState) int {
	count := 0
	for _, state := range states {
		if state == wanted {
			count++
		}
	}
	return count
}

func pickThread(random *rand.Rand, states []ThreadState, wanted ThreadState) int {
	candidates := make([]int, 0)
	for thread, state := range states {
		if state == wanted {
			candidates = append(candidates, thread)
		}
	}
	if len(candidates) == 0 {
		return random.Intn(len(states))
	}
	return candidates[random.Intn(len(candidates))]
}

func normalizeOutcome(outcome Outcome) Outcome {
	if outcome.Granted == nil {
		outcome.Granted = []int{}
	}
	return outcome
}
