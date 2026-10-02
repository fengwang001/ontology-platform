// Package scheduler implements a single-core discrete-step preemption
// threshold scheduling (PTS) model.
//
// Each tick is executed in three phases:
//  1. Decision: among ready tasks the candidate sigma is the one with the
//     highest effective priority e = p + min(Bmax, wt/W) (ties broken by
//     lower arrival sequence). The runner's shield is its threshold th, or
//     infinity once it has been preempted at least M times. Sigma is
//     dispatched when there is no runner, or preempts the runner when
//     e(sigma) is strictly greater than the shield.
//  2. Execution: the runner's remaining work decreases by one; every other
//     ready task's wait counter wt increases by one. A runner whose
//     remaining work reaches zero completes at time now+1 and leaves the
//     registry.
//  3. Clock: now increases by one.
package scheduler

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidConfig is returned by New when a constructor argument is
	// out of range.
	ErrInvalidConfig = errors.New("scheduler: invalid configuration")
	// ErrInvalidParam is returned by Add when a task parameter is invalid.
	ErrInvalidParam = errors.New("scheduler: invalid task parameters")
	// ErrDuplicate is returned by Add when the task id is already registered
	// (running or ready).
	ErrDuplicate = errors.New("scheduler: task id already exists")
	// ErrFull is returned by Add when the registry holds Tmax tasks.
	ErrFull = errors.New("scheduler: task registry full")
)

// InfShield is the shield value of a runner that has been preempted at
// least M times; no effective priority can exceed it.
const InfShield = 1 << 30

// maxBmax is the largest legal bonus cap, hence the number of bonus
// buckets is maxBmax+1.
const maxBmax = 1000

type task struct {
	id      int
	p       int
	th      int
	seq     uint64
	rem     int
	wt      int64
	pc      int
	bonus   int // current clamped bonus bucket min(Bmax, wt/W)
	heapIdx int // index inside its bonus bucket heap; -1 when not ready
}

// DecisionInfo describes the decision phase of the most recent Step.
type DecisionInfo struct {
	Tick      int64  // tick number k (the tick starts with now = k-1)
	RunnerID  int    // runner at the start of the tick, 0 when none
	SigmaID   int    // selected candidate id, 0 when no ready task
	SigmaE    int    // effective priority of the candidate
	Shield    int    // runner shield; InfShield when protected
	Preempted bool   // whether the runner was preempted this tick
	Reason    string // idle-dispatch | preempt | shielded | solo | idle
}

// bucketHeap is a binary max-heap of ready tasks ordered by
// (priority desc, seq asc). Tasks track their own index so removals are
// O(log n).
type bucketHeap struct {
	ts []*task
}

func higher(a, b *task) bool {
	if a.p != b.p {
		return a.p > b.p
	}
	return a.seq < b.seq
}

func (h *bucketHeap) push(t *task) {
	t.heapIdx = len(h.ts)
	h.ts = append(h.ts, t)
	h.up(t.heapIdx)
}

func (h *bucketHeap) remove(t *task) {
	i := t.heapIdx
	last := len(h.ts) - 1
	h.ts[i] = h.ts[last]
	h.ts[last] = nil
	h.ts = h.ts[:last]
	t.heapIdx = -1
	if i < last {
		h.ts[i].heapIdx = i
		h.fix(i)
	}
}

func (h *bucketHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !higher(h.ts[i], h.ts[parent]) {
			break
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *bucketHeap) down(i int) {
	for {
		best := i
		if l := 2*i + 1; l < len(h.ts) && higher(h.ts[l], h.ts[best]) {
			best = l
		}
		if r := 2*i + 2; r < len(h.ts) && higher(h.ts[r], h.ts[best]) {
			best = r
		}
		if best == i {
			break
		}
		h.swap(i, best)
		i = best
	}
}

func (h *bucketHeap) fix(i int) {
	if i > 0 && higher(h.ts[i], h.ts[(i-1)/2]) {
		h.up(i)
		return
	}
	h.down(i)
}

func (h *bucketHeap) swap(i, j int) {
	h.ts[i], h.ts[j] = h.ts[j], h.ts[i]
	h.ts[i].heapIdx = i
	h.ts[j].heapIdx = j
}

// Scheduler is a single-core discrete-step PTS model. All methods are safe
// for concurrent use; the result equals some serial execution order.
type Scheduler struct {
	mu sync.Mutex

	w    int64
	bmax int
	m    int
	tmax int

	now     int64
	seqNext uint64

	tasks   map[int]*task // registered tasks: running + ready
	ready   map[int]*task
	running *task

	buckets  [maxBmax + 1]bucketHeap // ready tasks per clamped bonus
	topBonus int                     // highest non-empty bucket, -1 when none

	examined int64 // candidates compared during decisions
	last     DecisionInfo
}

// New builds a scheduler. W is the bonus band width (1..1e6), Bmax the
// bonus cap (0..1000), M the preemption-protection count (1..1000) and
// Tmax the registry capacity (1..1e6). Out-of-range arguments reject the
// whole configuration with ErrInvalidConfig.
func New(W, Bmax, M, Tmax int) (*Scheduler, error) {
	if W < 1 || W > 1_000_000 || Bmax < 0 || Bmax > maxBmax ||
		M < 1 || M > 1000 || Tmax < 1 || Tmax > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Scheduler{
		w:        int64(W),
		bmax:     Bmax,
		m:        M,
		tmax:     Tmax,
		seqNext:  1,
		tasks:    make(map[int]*task),
		ready:    make(map[int]*task),
		topBonus: -1,
	}, nil
}

// Add registers a task arriving at the current time. The id must be a
// positive integer not currently registered (completed ids may be reused),
// p must be in [0,255], th in [p,255] and w in [1,1e6]. Errors are
// reported in the order: invalid parameter, duplicate id, registry full.
// A rejected Add changes neither tasks nor the sequence counter.
func (s *Scheduler) Add(id, p, th, w int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id < 1 || p < 0 || p > 255 || th < p || th > 255 || w < 1 || w > 1_000_000 {
		return ErrInvalidParam
	}
	if _, ok := s.tasks[id]; ok {
		return ErrDuplicate
	}
	if len(s.tasks) >= s.tmax {
		return ErrFull
	}
	t := &task{id: id, p: p, th: th, seq: s.seqNext, rem: w, heapIdx: -1}
	s.seqNext++
	s.tasks[id] = t
	s.ready[id] = t
	s.buckets[0].push(t)
	if s.topBonus < 0 {
		s.topBonus = 0
	}
	return nil
}

// Step advances one tick and returns the id of the task running during
// this tick (0 when idle) and the id of the task completing at the end of
// this tick (0 when none).
func (s *Scheduler) Step() (runID, completedID int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 1: decision, using wt values at the start of the tick.
	sigma := s.decide()
	runner := s.running
	shield := InfShield
	if runner != nil && runner.pc < s.m {
		shield = runner.th
	}
	info := DecisionInfo{Tick: s.now + 1, Shield: shield}
	if runner != nil {
		info.RunnerID = runner.id
	}
	if sigma != nil {
		info.SigmaID = sigma.id
		info.SigmaE = sigma.p + sigma.bonus
	}
	switch {
	case runner == nil && sigma != nil:
		s.dispatch(sigma)
		info.Reason = "idle-dispatch"
	case runner != nil && sigma != nil && info.SigmaE > shield:
		s.preemptLocked()
		s.dispatch(sigma)
		info.Preempted = true
		info.Reason = "preempt"
	case runner != nil && sigma != nil:
		info.Reason = "shielded"
	case runner != nil:
		info.Reason = "solo"
	default:
		info.Reason = "idle"
	}

	// Phase 2: execution.
	if s.running != nil {
		runID = s.running.id
		s.running.rem--
		if s.running.rem == 0 {
			completedID = s.running.id
			delete(s.tasks, s.running.id)
			s.running = nil
		}
	}
	for _, t := range s.ready {
		t.wt++
		if nb := s.bonusOf(t.wt); nb != t.bonus {
			s.buckets[t.bonus].remove(t)
			t.bonus = nb
			s.buckets[nb].push(t)
			if nb > s.topBonus {
				s.topBonus = nb
			}
		}
	}

	// Phase 3: clock.
	s.now++
	s.last = info
	return runID, completedID
}

// Now returns the current logical time.
func (s *Scheduler) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Examined returns the total number of ready-task candidates the decision
// phase has compared so far. It never scans all ready tasks.
func (s *Scheduler) Examined() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.examined
}

// LastDecision returns the decision record of the most recent Step.
func (s *Scheduler) LastDecision() DecisionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// decide picks the ready task with the highest effective priority
// e = p + bonus without scanning all ready tasks: ready tasks are bucketed
// by clamped bonus, each bucket heap keeps its best (priority, seq) task at
// the root, and only the roots of the (at most Bmax+1) non-empty buckets
// are compared. Buckets whose maximum attainable e (b + 255) is below the
// current best are skipped.
func (s *Scheduler) decide() *task {
	for s.topBonus >= 0 && len(s.buckets[s.topBonus].ts) == 0 {
		s.topBonus--
	}
	var best *task
	bestE := 0
	for b := s.topBonus; b >= 0; b-- {
		h := &s.buckets[b]
		if len(h.ts) == 0 {
			continue
		}
		if best != nil && b+255 < bestE {
			break
		}
		s.examined++
		cand := h.ts[0]
		if e := b + cand.p; best == nil || e > bestE || (e == bestE && cand.seq < best.seq) {
			best, bestE = cand, e
		}
	}
	return best
}

func (s *Scheduler) bonusOf(wt int64) int {
	b := wt / s.w
	if b > int64(s.bmax) {
		return s.bmax
	}
	return int(b)
}

// dispatch moves a ready task into the running slot and resets its wait
// counter.
func (s *Scheduler) dispatch(t *task) {
	s.buckets[t.bonus].remove(t)
	delete(s.ready, t.id)
	t.wt = 0
	t.bonus = 0
	s.running = t
}

// preemptLocked moves the runner back to ready, counting the preemption
// and resetting its wait counter.
func (s *Scheduler) preemptLocked() {
	t := s.running
	t.pc++
	t.wt = 0
	t.bonus = 0
	s.ready[t.id] = t
	s.buckets[0].push(t)
	if s.topBonus < 0 {
		s.topBonus = 0
	}
	s.running = nil
}
