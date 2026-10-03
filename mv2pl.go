package ontology

import (
	"errors"
	"sort"
	"sync"
)

// Mode is a lock mode held or requested on one key.
type Mode int

const (
	S Mode = iota // shared read lock
	X             // exclusive buffered write
	C             // certification lock during commit
)

// TxnStatus is the life-cycle state of a transaction.
type TxnStatus int

const (
	Active TxnStatus = iota
	Waiting
	Committing
	Committed
	Aborted
)

// EventKind distinguishes lock grants from commits.
type EventKind int

const (
	Grant EventKind = iota
	CommitEvent
)

// Event reports one lock grant or commit produced while processing a call.
type Event struct {
	Kind   EventKind
	Txn    int
	Key    int // 1-based; -1 for read-only commits
	Mode   Mode
	Value  int64 // granted S: value read; commit: committed value
	Status TxnStatus
}

// Result is the outcome of one call plus the events it produced in order.
type Result struct {
	OK        bool
	Rejected  bool
	RejectErr error
	Deadlock  bool
	Value     int64 // Read return value
	Txn       int   // Begin return value
	Status    TxnStatus
	Events    []Event
}

var (
	ErrInvalidK        = errors.New("K must be in 1..64")
	ErrUnknownTxn      = errors.New("unknown transaction id")
	ErrTxnNotActive    = errors.New("transaction must be active")
	ErrTxnNotAbortable = errors.New("transaction must be active, waiting or committing")
	ErrKeyOutOfRange   = errors.New("key out of range")
)

// entry is one queued request on one key.
type entry struct {
	txn  int
	mode Mode
	val  int64 // X requests carry the buffered write value
}

type keyState struct {
	committed int64
	granted   map[int]Mode // txn -> mode (at most one mode per txn per key)
	queue     []entry
}

type txn struct {
	status TxnStatus
	locks  map[int]Mode  // key -> currently held mode
	buf    map[int]int64 // key -> buffered write value
}

// Manager is the MV2PL certification lock manager.
type Manager struct {
	mu   sync.Mutex
	k    int
	keys []keyState
	txns map[int]*txn
	next int
}

// NewManager constructs a manager for K keys (1..64).
func NewManager(K int) (*Manager, error) {
	if K < 1 || K > 64 {
		return nil, ErrInvalidK
	}
	m := &Manager{
		k:    K,
		keys: make([]keyState, K),
		txns: make(map[int]*txn),
		next: 1,
	}
	for i := range m.keys {
		m.keys[i].granted = make(map[int]Mode)
	}
	return m, nil
}

// Begin starts a new transaction and returns its 1-based id.
func (m *Manager) Begin() Result {
	m.mu.Lock()
	id := m.next
	m.next++
	m.txns[id] = &txn{status: Active, locks: make(map[int]Mode), buf: make(map[int]int64)}
	m.mu.Unlock()
	return Result{OK: true, Txn: id, Status: Active}
}

// Read returns the committed value, or the transaction's own buffered value.
func (m *Manager) Read(t, k int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr, rerr := m.checkActive(t, k)
	if rerr != nil {
		return Result{Rejected: true, RejectErr: rerr}
	}
	ki := k - 1

	if mode, ok := tr.locks[ki]; ok {
		if mode == X {
			return Result{OK: true, Value: tr.buf[ki], Status: Active}
		}
		return Result{OK: true, Value: m.keys[ki].committed, Status: Active}
	}

	if !m.compatibleWithGranted(ki, S, t) || len(m.keys[ki].queue) > 0 {
		m.enqueue(ki, entry{txn: t, mode: S})
		if m.deadlocked(t) {
			ev := m.kill(t)
			return Result{Deadlock: true, Status: Aborted, Events: ev}
		}
		tr.status = Waiting
		return Result{OK: true, Status: Waiting}
	}

	m.keys[ki].granted[t] = S
	tr.locks[ki] = S
	return Result{OK: true, Value: m.keys[ki].committed, Status: Active}
}

// Write buffers x under an X lock.
func (m *Manager) Write(t, k int, x int64) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr, rerr := m.checkActive(t, k)
	if rerr != nil {
		return Result{Rejected: true, RejectErr: rerr}
	}
	ki := k - 1

	if mode, ok := tr.locks[ki]; ok && mode == X {
		tr.buf[ki] = x
		return Result{OK: true, Status: Active}
	}

	if m.compatibleWithGranted(ki, X, t) && len(m.keys[ki].queue) == 0 {
		m.keys[ki].granted[t] = X
		tr.locks[ki] = X
		tr.buf[ki] = x
		return Result{OK: true, Status: Active}
	}

	m.enqueue(ki, entry{txn: t, mode: X, val: x})
	if m.deadlocked(t) {
		ev := m.kill(t)
		return Result{Deadlock: true, Status: Aborted, Events: ev}
	}
	tr.status = Waiting
	return Result{OK: true, Status: Waiting}
}

// Commit certifies all buffered writes and releases all locks.
func (m *Manager) Commit(t int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr, rerr := m.checkActiveNoKey(t)
	if rerr != nil {
		return Result{Rejected: true, RejectErr: rerr}
	}

	var xKeys []int
	for ki, mode := range tr.locks {
		if mode == X {
			xKeys = append(xKeys, ki)
		}
	}
	sort.Ints(xKeys)

	if len(xKeys) == 0 {
		ev := m.commitAndRelease(t, nil)
		return Result{OK: true, Status: Committed, Events: ev}
	}

	tr.status = Committing
	remaining := len(xKeys)
	for _, ki := range xKeys {
		ks := &m.keys[ki]
		if m.compatibleWithGranted(ki, C, t) {
			// Immediate conversion: ignores the queue and replaces t's X.
			ks.granted[t] = C
			tr.locks[ki] = C
			remaining--
			continue
		}
		// Insert after the last conversion request, before all normal requests.
		pos := 0
		for pos < len(ks.queue) && ks.queue[pos].mode == C {
			pos++
		}
		m.enqueueAt(ki, pos, entry{txn: t, mode: C})
		if m.deadlocked(t) {
			ev := m.kill(t)
			return Result{Deadlock: true, Status: Aborted, Events: ev}
		}
	}

	if remaining == 0 {
		ev := m.commitAndRelease(t, xKeys)
		return Result{OK: true, Status: Committed, Events: ev}
	}
	return Result{OK: true, Status: Committing}
}

// Abort removes the transaction, releasing its locks and dropping buffers.
func (m *Manager) Abort(t int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr := m.txns[t]
	if tr == nil {
		return Result{Rejected: true, RejectErr: ErrUnknownTxn}
	}
	if tr.status != Active && tr.status != Waiting && tr.status != Committing {
		return Result{Rejected: true, RejectErr: ErrTxnNotAbortable}
	}
	ev := m.kill(t)
	return Result{OK: true, Status: Aborted, Events: ev}
}

func (m *Manager) checkActive(t, k int) (*txn, error) {
	tr := m.txns[t]
	if tr == nil {
		return nil, ErrUnknownTxn
	}
	if tr.status != Active {
		return nil, ErrTxnNotActive
	}
	if k < 1 || k > m.k {
		return nil, ErrKeyOutOfRange
	}
	return tr, nil
}

func (m *Manager) checkActiveNoKey(t int) (*txn, error) {
	tr := m.txns[t]
	if tr == nil {
		return nil, ErrUnknownTxn
	}
	if tr.status != Active {
		return nil, ErrTxnNotActive
	}
	return tr, nil
}

// compatibleModes reports whether two modes of different transactions
// conflict. Only S/S and S/X (in either order) are compatible; X/X clashes
// and C clashes with every mode.
func compatibleModes(a, b Mode) bool {
	return a != C && b != C && !(a == X && b == X)
}

func (m *Manager) compatibleWithGranted(ki int, mode Mode, self int) bool {
	for holder, hm := range m.keys[ki].granted {
		if holder == self {
			continue
		}
		if !compatibleModes(mode, hm) {
			return false
		}
	}
	return true
}

func (m *Manager) enqueue(ki int, e entry) {
	m.keys[ki].queue = append(m.keys[ki].queue, e)
}

func (m *Manager) enqueueAt(ki, pos int, e entry) {
	q := m.keys[ki].queue
	q = append(q, entry{})
	copy(q[pos+1:], q[pos:])
	q[pos] = e
	m.keys[ki].queue = q
}

// deadlocked checks whether requester t can reach itself over the waits-for
// graph derived from every current queue entry:
//   - a queue entry waits for every other-txn holder of a granted lock on its
//     key that conflicts with its requested mode;
//   - a queue entry waits for every other-txn entry queued ahead of it;
//   - wait edges hop through the holder's own queued entries
//     (holder node -> each of its queue entries).
//
// Graph nodes: queued entries encode (key,index); granted holders encode
// (txn, holderMark).
func (m *Manager) deadlocked(t int) bool {
	const (
		stride     = 1 << 20 // per-key queue index space
		holderMark = 1 << 30 // granted-holder nodes live above entry nodes
	)
	type graphNode = int

	var starts []graphNode
	for ki := range m.keys {
		for qi, e := range m.keys[ki].queue {
			if e.txn == t {
				starts = append(starts, ki*stride+qi)
			}
		}
	}

	visited := make(map[graphNode]bool)
	var stack []graphNode
	push := func(n graphNode) {
		if !visited[n] {
			visited[n] = true
			stack = append(stack, n)
		}
	}
	successors := func(n graphNode, emit func(graphNode)) {
		if n >= holderMark {
			holder := n - holderMark
			for ki := range m.keys {
				for qi, e := range m.keys[ki].queue {
					if e.txn == holder {
						emit(ki*stride + qi)
					}
				}
			}
			return
		}
		ki, qi := n/stride, n%stride
		ks := &m.keys[ki]
		cur := ks.queue[qi]
		for holder, hm := range ks.granted {
			if holder == cur.txn {
				continue
			}
			if !compatibleModes(cur.mode, hm) {
				emit(holderMark + holder)
			}
		}
		for p := 0; p < qi; p++ {
			if ks.queue[p].txn != cur.txn {
				emit(ki*stride + p)
			}
		}
	}

	for _, s := range starts {
		visited = map[graphNode]bool{}
		stack = stack[:0]
		successors(s, push)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			back := false
			successors(n, func(to graphNode) {
				if to == s {
					back = true
				}
				if !visited[to] {
					visited[to] = true
					stack = append(stack, to)
				}
			})
			if back {
				return true
			}
		}
	}
	return false
}

// kill removes all queue entries of t, releases all its locks and drops its
// buffers. Returns the grants and commits cascading from the releases,
// processed in strict ascending-key FIFO order.

// kill removes all queue entries of t, releases all its locks and drops its
// buffers. Returns the grants and commits cascading from the releases,
// processed in strict ascending-key FIFO order.
func (m *Manager) kill(t int) []Event {
	tr := m.txns[t]
	tr.status = Aborted

	pending := make(map[int]bool)
	for ki := range tr.locks {
		pending[ki] = true
		delete(m.keys[ki].granted, t)
	}
	for ki := range m.keys {
		q := m.keys[ki].queue[:0]
		for _, e := range m.keys[ki].queue {
			if e.txn != t {
				q = append(q, e)
			} else {
				pending[ki] = true
			}
		}
		m.keys[ki].queue = q
	}

	tr.locks = map[int]Mode{}
	tr.buf = map[int]int64{}

	return m.pump(pending)
}

// commitAndRelease installs buffered values as committed in ascending key
// order, releases every lock and pumps the waiters. An empty cKeys slice is a
// read-only commit.
func (m *Manager) commitAndRelease(t int, cKeys []int) []Event {
	tr := m.txns[t]

	var events []Event
	pending := make(map[int]bool)
	for ki := range tr.locks {
		pending[ki] = true
		delete(m.keys[ki].granted, t)
	}
	for _, ki := range cKeys {
		m.keys[ki].committed = tr.buf[ki]
		events = append(events, Event{
			Kind:   CommitEvent,
			Txn:    t,
			Key:    ki + 1,
			Mode:   C,
			Value:  m.keys[ki].committed,
			Status: Committing,
		})
	}
	if len(cKeys) == 0 {
		events = append(events, Event{
			Kind:   CommitEvent,
			Txn:    t,
			Key:    -1,
			Status: Committed,
		})
	}

	tr.locks = map[int]Mode{}
	tr.buf = map[int]int64{}
	tr.status = Committed

	events = append(events, m.pump(pending)...)
	return events
}

// pump repeatedly takes the smallest key whose queue may have become
// grantable and grants compatible requests from the queue head, stopping at
// the first incompatible request (strict FIFO). When a queued C grant lets a
// committing transaction finish, its commit events are emitted, its locks are
// released and their keys join the same pending set, so processing continues
// at the new smallest key.
func (m *Manager) pump(pending map[int]bool) []Event {
	var events []Event

	smallestPending := func() (int, bool) {
		ki := m.k
		found := false
		for k := range pending {
			if !found || k < ki {
				ki = k
				found = true
			}
		}
		return ki, found
	}

	for {
		ki, ok := smallestPending()
		if !ok {
			return events
		}
		delete(pending, ki)

		for len(m.keys[ki].queue) > 0 {
			head := m.keys[ki].queue[0]
			tr := m.txns[head.txn]
			if tr == nil {
				m.keys[ki].queue = m.keys[ki].queue[1:]
				continue
			}
			if !m.compatibleWithGranted(ki, head.mode, head.txn) {
				break
			}
			m.keys[ki].queue = m.keys[ki].queue[1:]

			switch head.mode {
			case S:
				m.keys[ki].granted[head.txn] = S
				tr.locks[ki] = S
				tr.status = Active
				events = append(events, Event{
					Kind: Grant, Txn: head.txn, Key: ki + 1, Mode: S,
					Value: m.keys[ki].committed, Status: Active,
				})
			case X:
				m.keys[ki].granted[head.txn] = X
				tr.locks[ki] = X
				tr.buf[ki] = head.val
				tr.status = Active
				events = append(events, Event{
					Kind: Grant, Txn: head.txn, Key: ki + 1, Mode: X,
					Status: Active,
				})
			case C:
				// t's X on this key becomes C. Certification completes once
				// every X key holds C.
				m.keys[ki].granted[head.txn] = C
				tr.locks[ki] = C
				if !m.hasQueuedC(head.txn) {
					var cKeys []int
					for lk, mode := range tr.locks {
						if mode == C {
							cKeys = append(cKeys, lk)
						}
					}
					sort.Ints(cKeys)
					for _, lk := range cKeys {
						m.keys[lk].committed = tr.buf[lk]
						events = append(events, Event{
							Kind: CommitEvent, Txn: head.txn, Key: lk + 1,
							Mode: C, Value: m.keys[lk].committed, Status: Committing,
						})
					}
					for lk := range tr.locks {
						pending[lk] = true
						delete(m.keys[lk].granted, head.txn)
					}
					tr.locks = map[int]Mode{}
					tr.buf = map[int]int64{}
					tr.status = Committed
					// Releases may have activated smaller keys; restart the
					// scan at the smallest pending key.
					goto nextRound
				}
			}
		}
	nextRound:
	}
}

func (m *Manager) hasQueuedC(t int) bool {
	for ki := range m.keys {
		for _, e := range m.keys[ki].queue {
			if e.txn == t && e.mode == C {
				return true
			}
		}
	}
	return false
}
