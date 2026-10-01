package buildgraph

import (
	"sort"
	"sync"
)

type edge struct {
	id        string
	cmd       string
	outputs   []string
	explicit  []string
	implicit  []string
	orderOnly []string
	restat    bool
}

type logEntry struct {
	cmd   string
	inMax int64
}

// Graph is a build graph with file mtimes, edges and a build log.
// All exported methods are safe for concurrent use; the effect of concurrent
// calls is always equivalent to some serial ordering.
type Graph struct {
	mu sync.RWMutex

	edges    map[string]*edge
	producer map[string]string // output path -> producing edge id
	users    map[string]map[string]bool
	mtimes   map[string]int64 // existing files only
	logs     map[string]logEntry

	// evalCount is reset at the start of every DirtySet and incremented once
	// per edge evaluated during that call.
	evalCount int
}

// New creates an empty Graph.
func New() *Graph {
	return &Graph{
		edges:    make(map[string]*edge),
		producer: make(map[string]string),
		users:    make(map[string]map[string]bool),
		mtimes:   make(map[string]int64),
		logs:     make(map[string]logEntry),
	}
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// AddEdge rejects, in this order: empty id, duplicate id, no outputs, any
// empty path, an output already produced by another edge, a path appearing as
// both output and input, and a dependency cycle (all three input kinds count).
func (g *Graph) AddEdge(id, cmd string, outputs, explicit, implicit, orderOnly []string, restat bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		return ErrEmptyID
	}
	if _, ok := g.edges[id]; ok {
		return ErrDuplicateID
	}
	if len(outputs) == 0 {
		return ErrNoOutputs
	}
	outputs = cloneStrings(outputs)
	explicit = cloneStrings(explicit)
	implicit = cloneStrings(implicit)
	orderOnly = cloneStrings(orderOnly)

	for _, p := range outputs {
		if p == "" {
			return ErrEmptyPath
		}
	}
	for _, list := range [][]string{explicit, implicit, orderOnly} {
		for _, p := range list {
			if p == "" {
				return ErrEmptyPath
			}
		}
	}

	for _, p := range outputs {
		if _, ok := g.producer[p]; ok {
			return ErrOutputOwned
		}
	}

	inputSet := make(map[string]bool)
	for _, list := range [][]string{explicit, implicit, orderOnly} {
		for _, p := range list {
			inputSet[p] = true
		}
	}
	for _, p := range outputs {
		if inputSet[p] {
			return ErrPathIsInAndOut
		}
	}

	if g.wouldCycle(outputs, inputSet) {
		return ErrCycle
	}

	e := &edge{
		id:        id,
		cmd:       cmd,
		outputs:   outputs,
		explicit:  explicit,
		implicit:  implicit,
		orderOnly: orderOnly,
		restat:    restat,
	}
	g.edges[id] = e
	for _, p := range outputs {
		g.producer[p] = id
	}
	for p := range inputSet {
		if g.users[p] == nil {
			g.users[p] = make(map[string]bool)
		}
		g.users[p][id] = true
	}
	return nil
}

// wouldCycle reports whether inserting the candidate edge creates a cycle.
// Let C be an existing consumer of a new output and T an existing producer of
// a new input. After insertion the new edge N sits between them: T -> N -> C.
// A cycle exists exactly when C can reach N or T through existing edges (then
// the chain closes via T -> N -> C). The forward search starts at every C and
// follows all three kinds of input dependencies; stepping onto a new output
// means reaching N.
func (g *Graph) wouldCycle(newOutputs []string, newInputs map[string]bool) bool {
	newOut := make(map[string]bool, len(newOutputs))
	for _, p := range newOutputs {
		newOut[p] = true
	}

	hasStart := false
	for p := range newInputs {
		if _, ok := g.producer[p]; ok {
			hasStart = true
			break
		}
	}

	var stack []string
	visited := make(map[string]bool)
	for _, p := range newOutputs {
		for id := range g.users[p] {
			if !visited[id] {
				visited[id] = true
				stack = append(stack, id)
			}
		}
	}
	if !hasStart || len(stack) == 0 {
		return false
	}
	for len(stack) > 0 {
		curID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		cur := g.edges[curID]
		for _, list := range [][]string{cur.explicit, cur.implicit, cur.orderOnly} {
			for _, p := range list {
				if newOut[p] {
					return true
				}
				pid, ok := g.producer[p]
				if !ok || visited[pid] {
					continue
				}
				visited[pid] = true
				stack = append(stack, pid)
			}
		}
	}
	return false
}

// SetMtime sets the modification time of path. t must be >= 1 and path non-empty.
func (g *Graph) SetMtime(path string, t int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if path == "" {
		return ErrEmptyPath
	}
	if t < 1 {
		return ErrInvalidMtime
	}
	g.mtimes[path] = t
	return nil
}

// Remove marks path as non-existent. Removing a non-existent path is a
// no-op success; the empty path is rejected.
func (g *Graph) Remove(path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if path == "" {
		return ErrEmptyPath
	}
	delete(g.mtimes, path)
	return nil
}

// Complete records a successful build of edge id: every output is assigned the
// given mtime (>= 1), and the log stores (current command, current max mtime
// of existing explicit/implicit inputs). Validation order: edge existence,
// exact output key set, mtime >= 1, all explicit/implicit inputs exist.
func (g *Graph) Complete(id string, outputs map[string]int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	e, ok := g.edges[id]
	if !ok {
		return ErrEdgeNotFound
	}
	want := make(map[string]bool, len(e.outputs))
	for _, p := range e.outputs {
		want[p] = true
	}
	if len(outputs) != len(want) {
		return ErrOutputSetMismatch
	}
	for p := range outputs {
		if !want[p] {
			return ErrOutputSetMismatch
		}
	}
	for _, p := range e.outputs {
		if outputs[p] < 1 {
			return ErrInvalidMtime
		}
	}
	for _, list := range [][]string{e.explicit, e.implicit} {
		for _, p := range list {
			if _, ok := g.mtimes[p]; !ok {
				return ErrMissingInput
			}
		}
	}

	for _, p := range e.outputs {
		g.mtimes[p] = outputs[p]
	}
	g.logs[id] = logEntry{cmd: e.cmd, inMax: g.inputMax(e)}
	return nil
}

// inputMax is the maximum mtime over existing explicit and implicit inputs;
// 0 when none exist. Caller must hold g.mu.
func (g *Graph) inputMax(e *edge) int64 {
	var max int64
	for _, list := range [][]string{e.explicit, e.implicit} {
		for _, p := range list {
			if t, ok := g.mtimes[p]; ok && t > max {
				max = t
			}
		}
	}
	return max
}

// dirtyReason evaluates self-dirtiness of one edge and returns the reason
// ("" when clean). Caller must hold g.mu.
func (g *Graph) selfDirtyReason(e *edge) string {
	log, logged := g.logs[e.id]
	switch {
	case !logged:
		return "no log entry"
	case log.cmd != e.cmd:
		return "command changed"
	}
	for _, p := range e.outputs {
		if _, ok := g.mtimes[p]; !ok {
			return "missing output " + p
		}
	}
	inMax := g.inputMax(e)
	if e.restat {
		if log.inMax < inMax {
			return "restat: logged inMax < current input max"
		}
		return ""
	}
	var minOut int64
	for i, p := range e.outputs {
		t := g.mtimes[p]
		if i == 0 || t < minOut {
			minOut = t
		}
	}
	if minOut < inMax {
		return "min output mtime < input max"
	}
	return ""
}

// DirtySet returns the ids of all dirty edges in the transitive dependency
// closure (all three input kinds) of the producing edges of targets, sorted by
// byte order. Source-file targets contribute no edges. It first rejects the
// first target (in argument order) absent from the graph, then the first
// missing source in the closure (edges sorted by id; within an edge explicit,
// implicit, order-only in index order). Every closure edge is evaluated at
// most once; the count is visible in EvalCount.
func (g *Graph) DirtySet(targets []string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.evalCount = 0

	for _, p := range targets {
		if _, isOut := g.producer[p]; !isOut {
			if len(g.users[p]) == 0 {
				return nil, &TargetError{Path: p}
			}
		}
	}

	closure := make(map[string]*edge)
	var stack []string
	seen := make(map[string]bool)
	for _, p := range targets {
		if id, ok := g.producer[p]; ok && !seen[id] {
			seen[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		e := g.edges[id]
		closure[id] = e
		for _, list := range [][]string{e.explicit, e.implicit, e.orderOnly} {
			for _, p := range list {
				pid, ok := g.producer[p]
				if ok && !seen[pid] {
					seen[pid] = true
					stack = append(stack, pid)
				}
			}
		}
	}

	ids := make([]string, 0, len(closure))
	for id := range closure {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		e := closure[id]
		for _, list := range [][]string{e.explicit, e.implicit, e.orderOnly} {
			for _, p := range list {
				if _, produced := g.producer[p]; produced {
					continue
				}
				if _, exists := g.mtimes[p]; !exists {
					return nil, &MissingSourceError{EdgeID: id, Path: p}
				}
			}
		}
	}

	// Kahn-style iterative evaluation over explicit/implicit producer deps.
	deps := make(map[string]map[string]bool, len(closure))
	children := make(map[string]map[string]bool, len(closure))
	indegree := make(map[string]int, len(closure))
	for id, e := range closure {
		d := make(map[string]bool)
		for _, list := range [][]string{e.explicit, e.implicit} {
			for _, p := range list {
				if pid, ok := g.producer[p]; ok {
					d[pid] = true
				}
			}
		}
		deps[id] = d
		indegree[id] = len(d)
		for pid := range d {
			if children[pid] == nil {
				children[pid] = make(map[string]bool)
			}
			children[pid][id] = true
		}
	}

	var queue []string
	for _, id := range ids {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	dirtyFromDep := make(map[string]bool, len(closure))
	dirty := make(map[string]bool, len(closure))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		g.evalCount++
		e := closure[id]
		isDirty := g.selfDirtyReason(e) != "" || dirtyFromDep[id]
		dirty[id] = isDirty
		for child := range children[id] {
			if isDirty {
				dirtyFromDep[child] = true
			}
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if dirty[id] {
			result = append(result, id)
		}
	}
	return result, nil
}

// EvalCount returns the number of edge evaluations performed by the most
// recent DirtySet call.
func (g *Graph) EvalCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.evalCount
}
