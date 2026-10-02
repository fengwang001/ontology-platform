// Package spotdrain implements a preemptible (Spot) instance reclamation
// pre-warning scheduler: on a reclaim notice it drains tasks according to
// the remaining grace period (natural completion, checkpointing, or
// discarding unsaved progress), and after the node expires it re-places
// evicted tasks by priority under an on-demand budget. All operations are
// deterministic and safe for concurrent use.
package spotdrain

import (
	"errors"
	"sort"
	"sync"
)

// NodeKind is the capacity type of a node.
type NodeKind int

const (
	// Spot is preemptible capacity subject to reclaim notices.
	Spot NodeKind = iota
	// OnDemand is paid capacity charged against the budget at placement.
	OnDemand
)

// TaskState is the lifecycle state of a task.
type TaskState int

const (
	// Pending means the task waits for placement.
	Pending TaskState = iota
	// Running means the task occupies a slot on a node.
	Running
	// Completed means the task reported full progress and released its slot.
	Completed
)

var (
	ErrInvalidConfig   = errors.New("spotdrain: invalid config")
	ErrInvalidParam    = errors.New("spotdrain: invalid parameter")
	ErrNodeExists      = errors.New("spotdrain: node already exists")
	ErrNodeNotFound    = errors.New("spotdrain: node not found")
	ErrNotSpot         = errors.New("spotdrain: node is not spot")
	ErrAlreadyNoticed  = errors.New("spotdrain: node already noticed")
	ErrTaskExists      = errors.New("spotdrain: task already exists")
	ErrTaskNotFound    = errors.New("spotdrain: task not found")
	ErrTaskNotRunning  = errors.New("spotdrain: task not running")
	ErrInvalidProgress = errors.New("spotdrain: invalid progress")
	ErrNotNoticed      = errors.New("spotdrain: node not noticed")
	ErrNotExpired      = errors.New("spotdrain: node not expired")
)

const (
	maxG     = 1_000_000
	maxPod   = 1_000_000
	maxBud   = 1_000_000_000_000
	maxSlots = 1000
	maxPrio  = 255
	maxWork  = 1_000_000_000
	// maxNow bounds the timestamp accepted by Notice and Expire so that
	// now+G never overflows.
	maxNow = 1_000_000_000_000_000
)

type node struct {
	id      int64
	kind    NodeKind
	slots   int
	noticed bool
	dl      int64
	running map[int64]struct{}
}

type task struct {
	id     int64
	prio   int64
	w      int64
	iv     int64
	ck     int64
	p      int64
	cp     int64
	rs     int64
	state  TaskState
	nodeID int64
}

// TaskInfo is a read-only snapshot of a task.
type TaskInfo struct {
	State  TaskState
	P      int64
	Cp     int64
	Rs     int64
	NodeID int64 // valid only when State == Running
}

// NodeInfo is a read-only snapshot of a node.
type NodeInfo struct {
	Kind    NodeKind
	Slots   int
	Used    int
	Noticed bool
}

// Scheduler drains and re-places tasks under Spot reclaim notices.
// The zero value is not usable; construct with NewScheduler.
type Scheduler struct {
	mu    sync.Mutex
	g     int64
	pod   int64
	bud   int64
	nodes map[int64]*node
	tasks map[int64]*task
	rwk   int64
}

// NewScheduler validates the configuration; out-of-range parameters reject
// the whole configuration and no scheduler is created.
func NewScheduler(g, pod, bud int64) (*Scheduler, error) {
	if g < 1 || g > maxG || pod < 1 || pod > maxPod || bud < 0 || bud > maxBud {
		return nil, ErrInvalidConfig
	}
	return &Scheduler{
		g:     g,
		pod:   pod,
		bud:   bud,
		nodes: make(map[int64]*node),
		tasks: make(map[int64]*task),
	}, nil
}

// AddNode registers a node. Invalid parameters are reported before
// duplicate ids; a rejected call changes nothing.
func (s *Scheduler) AddNode(id int64, kind NodeKind, slots int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id <= 0 || (kind != Spot && kind != OnDemand) || slots < 1 || slots > maxSlots {
		return ErrInvalidParam
	}
	if _, ok := s.nodes[id]; ok {
		return ErrNodeExists
	}
	s.nodes[id] = &node{
		id:      id,
		kind:    kind,
		slots:   slots,
		running: make(map[int64]struct{}),
	}
	return nil
}

// AddTask registers a task in the pending state with p=0, cp=0, rs=0.
// Invalid parameters are reported before duplicate ids.
func (s *Scheduler) AddTask(id, prio, w, iv, ck int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id <= 0 || prio < 0 || prio > maxPrio ||
		w < 1 || w > maxWork || iv < 1 || iv > maxWork || ck < 1 || ck > maxWork {
		return ErrInvalidParam
	}
	if _, ok := s.tasks[id]; ok {
		return ErrTaskExists
	}
	s.tasks[id] = &task{id: id, prio: prio, w: w, iv: iv, ck: ck, state: Pending}
	return nil
}

// Place places every placeable pending task in (prio desc, id asc) order.
// A task prefers the un-noticed Spot node with the most free slots (ties by
// smaller id); tasks with rs >= 2 have no Spot candidates. Without a Spot
// candidate the task goes to the smallest-id OnDemand node with a free slot
// if (w-cp)*Pod fits the remaining budget; otherwise it stays pending
// without blocking later tasks.
func (s *Scheduler) Place() {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := make([]*task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.state == Pending {
			pending = append(pending, t)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].prio != pending[j].prio {
			return pending[i].prio > pending[j].prio
		}
		return pending[i].id < pending[j].id
	})
	for _, t := range pending {
		if n := s.spotCandidate(t); n != nil {
			s.assign(n, t)
			continue
		}
		n := s.onDemandCandidate()
		if n == nil {
			continue
		}
		cost := (t.w - t.cp) * s.pod
		if cost > s.bud {
			continue
		}
		s.bud -= cost
		s.assign(n, t)
	}
}

// spotCandidate returns the un-noticed Spot node with the most free slots
// (ties by smaller id), or nil when the task has rs >= 2 or no Spot node
// has a free slot.
func (s *Scheduler) spotCandidate(t *task) *node {
	if t.rs >= 2 {
		return nil
	}
	var best *node
	for _, n := range s.nodes {
		if n.kind != Spot || n.noticed || len(n.running) >= n.slots {
			continue
		}
		if best == nil ||
			n.slots-len(n.running) > best.slots-len(best.running) ||
			(n.slots-len(n.running) == best.slots-len(best.running) && n.id < best.id) {
			best = n
		}
	}
	return best
}

// onDemandCandidate returns the smallest-id OnDemand node with a free slot.
func (s *Scheduler) onDemandCandidate() *node {
	var best *node
	for _, n := range s.nodes {
		if n.kind != OnDemand || len(n.running) >= n.slots {
			continue
		}
		if best == nil || n.id < best.id {
			best = n
		}
	}
	return best
}

func (s *Scheduler) assign(n *node, t *task) {
	n.running[t.id] = struct{}{}
	t.state = Running
	t.nodeID = n.id
}

// Report records progress p' for a running task: cp advances to
// max(cp, floor(p'/iv)*iv); p' == w completes the task and frees its slot.
// Errors are checked in the order: unknown task, not running, bad progress.
func (s *Scheduler) Report(id, p int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if t.state != Running {
		return ErrTaskNotRunning
	}
	if p < t.p || p > t.w {
		return ErrInvalidProgress
	}
	t.p = p
	if cp := p / t.iv * t.iv; cp > t.cp {
		t.cp = cp
	}
	if p == t.w {
		delete(s.nodes[t.nodeID].running, t.id)
		t.state = Completed
		t.nodeID = 0
	}
	return nil
}

// Notice handles a reclaim pre-warning for Spot node n at time now; the
// deadline is dl = now+G. Each running task is classified:
//   - natural completion: remaining w-p <= G (equality included);
//   - saved: unsaved u = p-cp is 0 (no time cost), or its checkpoint cost
//     ck fits the cumulative budget when processing u > 0 tasks in
//     (u desc, id asc) order while the cumulative cost stays <= G (the task
//     is checkpointed: cp = p);
//   - dropped: the first task whose cumulative cost exceeds G, together
//     with every task after it (no skipping to smaller tasks).
//
// The three returned lists are sorted by task id. The node stops accepting
// new placements. Errors are checked in the order: bad now, unknown node,
// not Spot, already noticed.
func (s *Scheduler) Notice(n, now int64) (completed, saved, dropped []int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	completed, saved, dropped = []int64{}, []int64{}, []int64{}
	if now < 0 || now > maxNow {
		return completed, saved, dropped, ErrInvalidParam
	}
	nd, ok := s.nodes[n]
	if !ok {
		return completed, saved, dropped, ErrNodeNotFound
	}
	if nd.kind != Spot {
		return completed, saved, dropped, ErrNotSpot
	}
	if nd.noticed {
		return completed, saved, dropped, ErrAlreadyNoticed
	}
	nd.noticed = true
	nd.dl = now + s.g

	type pending struct {
		id int64
		u  int64
		ck int64
	}
	var unsaved []pending
	for id := range nd.running {
		t := s.tasks[id]
		if t.w-t.p <= s.g {
			completed = append(completed, id)
			continue
		}
		u := t.p - t.cp
		if u == 0 {
			saved = append(saved, id)
			continue
		}
		unsaved = append(unsaved, pending{id: id, u: u, ck: t.ck})
	}
	sort.Slice(unsaved, func(i, j int) bool {
		if unsaved[i].u != unsaved[j].u {
			return unsaved[i].u > unsaved[j].u
		}
		return unsaved[i].id < unsaved[j].id
	})
	var acc int64
	failed := false
	for _, pd := range unsaved {
		if failed {
			dropped = append(dropped, pd.id)
			continue
		}
		acc += pd.ck
		if acc <= s.g {
			s.tasks[pd.id].cp = s.tasks[pd.id].p
			saved = append(saved, pd.id)
		} else {
			failed = true
			dropped = append(dropped, pd.id)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i] < completed[j] })
	sort.Slice(saved, func(i, j int) bool { return saved[i] < saved[j] })
	sort.Slice(dropped, func(i, j int) bool { return dropped[i] < dropped[j] })
	return completed, saved, dropped, nil
}

// Expire removes noticed node n once now >= dl. Tasks still on it (including
// naturally-completing ones that never reported completion) return to the
// pending state with rs incremented and p rolled back to cp. It returns the
// rework (p before rollback minus cp) of every returned task, including
// zero-rework entries. Errors are checked in the order: bad now, unknown
// node, not noticed, not yet expired.
func (s *Scheduler) Expire(n, now int64) (map[int64]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || now > maxNow {
		return nil, ErrInvalidParam
	}
	nd, ok := s.nodes[n]
	if !ok {
		return nil, ErrNodeNotFound
	}
	if !nd.noticed {
		return nil, ErrNotNoticed
	}
	if now < nd.dl {
		return nil, ErrNotExpired
	}
	rework := make(map[int64]int64, len(nd.running))
	for id := range nd.running {
		t := s.tasks[id]
		rework[id] = t.p - t.cp
		s.rwk += t.p - t.cp
		t.rs++
		t.p = t.cp
		t.state = Pending
		t.nodeID = 0
	}
	delete(s.nodes, n)
	return rework, nil
}

// Budget returns the remaining on-demand budget (monotonically decreasing,
// never negative).
func (s *Scheduler) Budget() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bud
}

// TotalRework returns the sum of the rework returned by all Expire calls.
func (s *Scheduler) TotalRework() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rwk
}

// TaskInfo returns a snapshot of task id.
func (s *Scheduler) TaskInfo(id int64) (TaskInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return TaskInfo{}, false
	}
	return TaskInfo{State: t.state, P: t.p, Cp: t.cp, Rs: t.rs, NodeID: t.nodeID}, true
}

// NodeInfo returns a snapshot of node id.
func (s *Scheduler) NodeInfo(id int64) (NodeInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return NodeInfo{}, false
	}
	return NodeInfo{Kind: n.kind, Slots: n.slots, Used: len(n.running), Noticed: n.noticed}, true
}
