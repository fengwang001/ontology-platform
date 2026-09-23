// Package sched is a deterministic single-CPU discrete-step scheduler with
// transitive priority inheritance and deadlock rejection.
package sched

import (
	"errors"
	"fmt"
	"sync"

	"ontology/inherit"
	"ontology/mutex"
	"ontology/prog"
	"ontology/wfg"
)

// Resource limits.
const (
	MaxTasks   = 10000
	MaxWaiters = 256
)

var (
	// ErrDeadlock is returned (and recorded) when a Lock would close a cycle.
	ErrDeadlock = errors.New("lock would deadlock")
	// ErrTimeout marks a LockTimeout whose deadline elapsed.
	ErrTimeout = errors.New("lock timeout")
	// ErrLimit marks exceeding MaxTasks or a mutex waiter cap.
	ErrLimit = errors.New("resource limit exceeded")
	// ErrAborted marks a task that executed Done while holding mutexes.
	ErrAborted = errors.New("task finished while holding locks")
)

// DeadlockError carries the cycle, from requester along wait direction.
type DeadlockError struct {
	Cycle []string
}

func (e *DeadlockError) Error() string   { return fmt.Sprintf("%v: %v", ErrDeadlock, e.Cycle) }
func (e *DeadlockError) Is(t error) bool { return t == ErrDeadlock }

// LimitError names the violated resource limit.
type LimitError struct{ What string }

func (e *LimitError) Error() string   { return ErrLimit.Error() + ": " + e.What }
func (e *LimitError) Is(t error) bool { return t == ErrLimit }

// Notice flags a mutex received from an aborted predecessor.
type Notice struct {
	Mu      string
	From    string
	Aborted bool
}

// Event is one trace line.
type Event struct {
	Step int
	Task string
	Op   string
	Arg  string
	Eff  map[string]int
}

type task struct {
	id     string
	base   int
	pr     *prog.Program
	pc     int
	work   int
	cur    prog.Instr
	notes  []Notice
	err    error
	done   bool
	ticket int
}

// Scheduler runs one instruction of one task per step.
type Scheduler struct {
	mu    sync.Mutex
	eng   *inherit.Engine
	g     *wfg.Graph
	mus   map[string]bool
	tasks map[string]*task
	order []string
	step  int
	tick  int
	trace []Event
}

// New creates an empty scheduler.
func New() *Scheduler {
	return &Scheduler{
		eng:   inherit.NewEngine(),
		g:     wfg.New(),
		mus:   map[string]bool{},
		tasks: map[string]*task{},
	}
}

// Submit registers a task. It is goroutine-safe; submission order is the order
// in which goroutines acquire the internal submission lock.
func (s *Scheduler) Submit(id string, base int, p *prog.Program) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[id]; exists {
		return fmt.Errorf("sched: duplicate task %q", id)
	}
	if len(s.tasks) >= MaxTasks {
		return &LimitError{What: "MaxTasks"}
	}
	s.eng.AddTask(id, base)
	t := &task{id: id, base: base, pr: p, ticket: s.tick}
	s.tick++
	s.tasks[id] = t
	s.order = append(s.order, id)
	return nil
}

// Eff exposes a task's current effective priority.
func (s *Scheduler) Eff(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eng.Eff(id)
}

// Trace returns a copy of the per-step trace.
func (s *Scheduler) Trace() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.trace))
	copy(out, s.trace)
	return out
}

func (s *Scheduler) snapshot() map[string]int {
	m := map[string]int{}
	for id := range s.tasks {
		m[id] = s.eng.Eff(id)
	}
	return m
}

// pick selects the highest-eff ready task, ties by oldest round-robin ticket.
func (s *Scheduler) pick() *task {
	var best *task
	for _, id := range s.order {
		t := s.tasks[id]
		if t.done || s.eng.WaitingOn(id) != "" {
			continue
		}
		if best == nil || s.eng.Eff(id) > s.eng.Eff(best.id) ||
			s.eng.Eff(id) == s.eng.Eff(best.id) && t.ticket < best.ticket {
			best = t
		}
	}
	return best
}

func (s *Scheduler) ensureMu(name string) {
	if !s.mus[name] {
		s.mus[name] = true
		s.eng.EnsureMu(name)
	}
}

// Step executes one instruction of the chosen task and returns false when idle.
func (s *Scheduler) Step() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Handoff (Unlock/abort) already precedes expiry inside each step.
	if t := s.pick(); t != nil {
		s.runOne(t)
	} else {
		// Blocked-only system: still advance timers so timeouts resolve.
		s.step++
		exp := s.eng.Expire(s.step)
		for _, id := range exp {
			if tk := s.tasks[id]; tk != nil {
				tk.err = ErrTimeout
				tk.pc++
			}
		}
		s.record("", "", "", s.snapshot())
		if len(exp) == 0 {
			return false
		}
		return true
	}
	// Expire other waiters at this step end (granted head waiters excluded).
	for _, id := range s.eng.Expire(s.step) {
		if tk := s.tasks[id]; tk != nil {
			tk.err = ErrTimeout
			tk.pc++
		}
	}
	return true
}

func (s *Scheduler) record(id, op, arg string, eff map[string]int) {
	s.trace = append(s.trace, Event{Step: s.step, Task: id, Op: op, Arg: arg, Eff: eff})
}

func (s *Scheduler) runOne(t *task) {
	s.step++
	t.ticket = s.tick
	s.tick++
	if t.work > 0 {
		t.work--
		s.record(t.id, "Work", fmt.Sprint(t.work), s.snapshot())
		if t.work == 0 {
			t.pc++
		}
		return
	}
	in := t.pr.Instrs[t.pc]
	t.cur = in
	s.exec(t, in)
	s.record(t.id, opName(in.Op), in.Mu+argN(in), s.snapshot())
}

func argN(in prog.Instr) string {
	if in.Op == prog.Work {
		return fmt.Sprintf(" %d", in.N)
	}
	if in.Op == prog.LockTimeout {
		return fmt.Sprintf(" %d", in.N)
	}
	return ""
}

func opName(o prog.Op) string {
	return [...]string{"Lock", "LockTimeout", "Unlock", "Work", "Done"}[o]
}

func (s *Scheduler) exec(t *task, in prog.Instr) {
	switch in.Op {
	case prog.Work:
		t.work = in.N
	case prog.Lock, prog.LockTimeout:
		s.doLock(t, in)
	case prog.Unlock:
		s.doUnlock(t, in)
	case prog.Done:
		s.doDone(t)
	}
}

func (s *Scheduler) doLock(t *task, in prog.Instr) {
	s.ensureMu(in.Mu)
	timeout := 0
	if in.Op == prog.LockTimeout {
		timeout = in.N
	}
	if s.eng.Free(in.Mu) {
		s.eng.Acquire(t.id, in.Mu)
		t.pc++
		return
	}
	owner := s.eng.Owner(in.Mu)
	if cyc := s.g.Cycle(t.id, owner); cyc != nil {
		t.err = &DeadlockError{Cycle: cyc}
		t.pc++
		return
	}
	if s.eng.WaiterLen(in.Mu) >= MaxWaiters {
		t.err = &LimitError{What: "MaxWaiters:" + in.Mu}
		t.pc++
		return
	}
	s.g.Add(t.id, owner, in.Mu)
	s.eng.BeginWait(t.id, in.Mu, s.step, timeout)
	t.pc++
}

func (s *Scheduler) doUnlock(t *task, in prog.Instr) {
	to, err := s.eng.Unlock(t.id, in.Mu)
	if err != nil {
		t.err = err
		return
	}
	if to != "" {
		s.g.Remove(to)
		rt := s.tasks[to]
		rt.notes = append(rt.notes, Notice{Mu: in.Mu, From: t.id})
		rt.pc++
	}
	t.pc++
}

func (s *Scheduler) doDone(t *task) {
	if len(s.eng.HeldBy(t.id)) > 0 {
		hs := s.eng.Abort(t.id)
		for _, h := range hs {
			s.g.Remove(h.To)
			rt := s.tasks[h.To]
			rt.notes = append(rt.notes,
				Notice{Mu: h.Mu, From: h.From, Aborted: true})
			rt.pc++
		}
		t.err = ErrAborted
	}
	t.done = true
	t.pc++
}

// Run executes up to max steps, stopping early when idle.
func (s *Scheduler) Run(maxSteps int) {
	for i := 0; i < maxSteps; i++ {
		if !s.Step() {
			return
		}
	}
}

// Err returns the terminal error recorded for a task, if any.
func (s *Scheduler) Err(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tasks[id]; t != nil {
		return t.err
	}
	return nil
}

// Done reports whether a task reached its final instruction.
func (s *Scheduler) Done(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	return t != nil && t.done
}

// Notices returns and clears the predecessor notices delivered to a task.
func (s *Scheduler) Notices(id string) []Notice {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil {
		return nil
	}
	n := t.notes
	t.notes = nil
	return n
}

var _ = mutex.New
