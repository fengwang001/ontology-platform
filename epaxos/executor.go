// Package epaxos implements an EPaxos-style execution scheduler for
// committed instances. Instances are identified by (replica, slot) and
// carry a sequence number plus a dependency set. Execute orders the
// committed-but-unexecuted instances by contracting the dependency graph
// into strongly connected components: components are emitted
// dependencies-first, ties between ready components are broken by the
// smallest (seq, replica, slot) head member, and members inside one
// component are emitted in (seq, replica, slot) order.
package epaxos

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Instance identifies a committed instance by replica id and slot number.
type Instance struct {
	R int // replica id, 0 <= R < N
	I int // slot number, >= 1
}

func (in Instance) String() string { return fmt.Sprintf("(%d,%d)", in.R, in.I) }

// Distinguishable failure reasons for NewExecutor and Commit.
var (
	ErrInvalidParams   = errors.New("epaxos: N and Cap must be positive integers")
	ErrInvalidInstance = errors.New("epaxos: replica out of range or slot < 1")
	ErrInvalidSeq      = errors.New("epaxos: seq must be a positive integer")
	ErrSelfDependency  = errors.New("epaxos: instance depends on itself")
	ErrDuplicateDep    = errors.New("epaxos: dependency list contains duplicates")
	ErrConflict        = errors.New("epaxos: instance already committed with different seq or deps")
	ErrCapExceeded     = errors.New("epaxos: unexecuted committed instance count reached Cap")
)

// record is the remembered state of one committed instance. Executed
// instances keep their seq and deps for conflict checks on re-commit.
type record struct {
	seq      int
	deps     []Instance // canonical: sorted by (R, I), deduplicated
	executed bool
}

// Executor orders committed instances for execution. All methods are safe
// for concurrent use; the result is equivalent to some serial order.
type Executor struct {
	mu         sync.Mutex
	n          int
	maxPending int
	pending    int // committed but not yet executed
	recs       map[Instance]*record
}

// NewExecutor builds an Executor for n replicas that holds at most cap
// committed-but-unexecuted instances. Both must be positive.
func NewExecutor(n, cap int) (*Executor, error) {
	if n <= 0 || cap <= 0 {
		return nil, fmt.Errorf("%w: got N=%d Cap=%d", ErrInvalidParams, n, cap)
	}
	return &Executor{n: n, maxPending: cap, recs: make(map[Instance]*record)}, nil
}

// Commit registers a committed instance. Re-committing the same instance
// with an identical seq and dependency set (order ignored) is a legal
// no-op, even after the instance has executed. Any rejection leaves the
// state untouched. Checks run in a fixed order and report the first hit:
// invalid instance/dependency, invalid seq, self dependency, duplicate
// dependency, conflicting re-commit, capacity exceeded.
func (e *Executor) Commit(inst Instance, seq int, deps []Instance) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.valid(inst) {
		return fmt.Errorf("%w: instance %v", ErrInvalidInstance, inst)
	}
	for _, d := range deps {
		if !e.valid(d) {
			return fmt.Errorf("%w: dependency %v of %v", ErrInvalidInstance, d, inst)
		}
	}
	if seq <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidSeq, seq)
	}
	for _, d := range deps {
		if d == inst {
			return fmt.Errorf("%w: %v", ErrSelfDependency, inst)
		}
	}
	canon, dup := canonicalDeps(deps)
	if dup {
		return fmt.Errorf("%w: %v", ErrDuplicateDep, inst)
	}
	if rec, ok := e.recs[inst]; ok {
		if rec.seq == seq && slices.Equal(rec.deps, canon) {
			return nil // idempotent no-op
		}
		return fmt.Errorf("%w: %v", ErrConflict, inst)
	}
	if e.pending >= e.maxPending {
		return fmt.Errorf("%w: %v", ErrCapExceeded, inst)
	}
	e.recs[inst] = &record{seq: seq, deps: canon}
	e.pending++
	return nil
}

// Execute emits every executable committed-but-unexecuted instance in a
// deterministic order and marks them executed. Blocked instances (some
// instance reachable along dependency edges is not committed yet) stay
// pending for later calls. The returned slice is freshly allocated.
func (e *Executor) Execute() []Instance {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Edges u->d over committed-unexecuted instances only: executed deps
	// are satisfied, uncommitted deps make u (transitively) blocked.
	adj := make(map[Instance][]Instance)
	rev := make(map[Instance][]Instance)
	bad := make(map[Instance]bool)
	var nodes []Instance
	for inst, rec := range e.recs {
		if rec.executed {
			continue
		}
		nodes = append(nodes, inst)
		for _, d := range rec.deps {
			dr, ok := e.recs[d]
			switch {
			case !ok:
				bad[inst] = true
			case !dr.executed:
				adj[inst] = append(adj[inst], d)
				rev[d] = append(rev[d], inst)
			}
		}
	}
	if len(nodes) == 0 {
		return nil
	}

	// Blocked = can reach a node with an uncommitted dependency.
	blocked := make(map[Instance]bool)
	queue := make([]Instance, 0, len(bad))
	for u := range bad {
		if !blocked[u] {
			blocked[u] = true
			queue = append(queue, u)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, p := range rev[u] {
			if !blocked[p] {
				blocked[p] = true
				queue = append(queue, p)
			}
		}
	}

	execSet := make(map[Instance]bool)
	var execNodes []Instance
	for _, u := range nodes {
		if !blocked[u] {
			execSet[u] = true
			execNodes = append(execNodes, u)
		}
	}
	if len(execNodes) == 0 {
		return nil
	}

	eadj := make(map[Instance][]Instance)
	for _, u := range execNodes {
		for _, d := range adj[u] {
			if execSet[d] {
				eadj[u] = append(eadj[u], d)
			}
		}
		slices.SortFunc(eadj[u], compareInst)
	}
	comps := tarjan(execNodes, eadj)

	compOf := make(map[Instance]int, len(execNodes))
	for ci, members := range comps {
		for _, m := range members {
			compOf[m] = ci
		}
	}
	succ := make([]map[int]bool, len(comps)) // dep direction: c depends on succ[c]
	pred := make([]map[int]bool, len(comps))
	for c := range comps {
		succ[c] = make(map[int]bool)
		pred[c] = make(map[int]bool)
	}
	for u, ds := range eadj {
		cu := compOf[u]
		for _, d := range ds {
			cd := compOf[d]
			if cu != cd {
				succ[cu][cd] = true
				pred[cd][cu] = true
			}
		}
	}

	lessExec := func(a, b Instance) bool {
		ra, rb := e.recs[a], e.recs[b]
		if ra.seq != rb.seq {
			return ra.seq < rb.seq
		}
		return compareInst(a, b) < 0
	}
	head := make([]Instance, len(comps))
	for c, members := range comps {
		h := members[0]
		for _, m := range members[1:] {
			if lessExec(m, h) {
				h = m
			}
		}
		head[c] = h
	}

	out := make([]Instance, 0, len(execNodes))
	done := make([]bool, len(comps))
	for remaining := len(comps); remaining > 0; remaining-- {
		best := -1
		for c := range comps {
			if done[c] || len(succ[c]) > 0 {
				continue
			}
			if best == -1 || lessExec(head[c], head[best]) {
				best = c
			}
		}
		members := comps[best]
		slices.SortFunc(members, func(a, b Instance) int {
			if lessExec(a, b) {
				return -1
			}
			return 1
		})
		out = append(out, members...)
		done[best] = true
		for p := range pred[best] {
			delete(succ[p], best)
		}
	}

	for _, u := range out {
		e.recs[u].executed = true
	}
	e.pending -= len(out)
	return out
}

// Pending returns the committed-but-unexecuted instances sorted by
// (replica, slot) ascending.
func (e *Executor) Pending() []Instance {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Instance, 0, e.pending)
	for inst, rec := range e.recs {
		if !rec.executed {
			out = append(out, inst)
		}
	}
	slices.SortFunc(out, compareInst)
	return out
}

func (e *Executor) valid(inst Instance) bool {
	return inst.R >= 0 && inst.R < e.n && inst.I >= 1
}

func compareInst(a, b Instance) int {
	if c := cmp.Compare(a.R, b.R); c != 0 {
		return c
	}
	return cmp.Compare(a.I, b.I)
}

// canonicalDeps returns a sorted copy of deps and reports duplicates.
func canonicalDeps(deps []Instance) ([]Instance, bool) {
	s := slices.Clone(deps)
	slices.SortFunc(s, compareInst)
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			return nil, true
		}
	}
	return s, false
}

// tarjan returns the strongly connected components of the graph. Node
// iteration order is fixed so the result is deterministic.
func tarjan(nodes []Instance, adj map[Instance][]Instance) [][]Instance {
	index := make(map[Instance]int, len(nodes))
	low := make(map[Instance]int, len(nodes))
	onStack := make(map[Instance]bool, len(nodes))
	var stack []Instance
	var comps [][]Instance
	counter := 0

	var strongconnect func(u Instance)
	strongconnect = func(u Instance) {
		index[u] = counter
		low[u] = counter
		counter++
		stack = append(stack, u)
		onStack[u] = true
		for _, v := range adj[u] {
			if _, seen := index[v]; !seen {
				strongconnect(v)
				if low[v] < low[u] {
					low[u] = low[v]
				}
			} else if onStack[v] && index[v] < low[u] {
				low[u] = index[v]
			}
		}
		if low[u] == index[u] {
			var comp []Instance
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == u {
					break
				}
			}
			comps = append(comps, comp)
		}
	}

	sorted := slices.Clone(nodes)
	slices.SortFunc(sorted, compareInst)
	for _, u := range sorted {
		if _, seen := index[u]; !seen {
			strongconnect(u)
		}
	}
	return comps
}
