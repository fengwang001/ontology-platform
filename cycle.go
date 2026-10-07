package ontology

import "sort"

// CycleResult is the outcome of a cycle detection call.
type CycleResult struct {
	HasCycle bool
	// Evidence is a minimal set of objects that itself forms a directed
	// cycle in the effective subgraph. It contains no object unrelated to
	// that cycle.
	Evidence []string
}

// CycleStats are internal cost metrics. They are intentionally returned by a
// separate internal entry point rather than exposed to ordinary callers.
type CycleStats struct {
	// VisibleObjects is the size of the existence-filtered object set.
	VisibleObjects int
	// ActiveLinks is the number of links surviving both permission filters.
	ActiveLinks int
	// NodesExamined and LinksExamined count the work actually performed:
	// objects dequeued and outgoing arcs inspected. Adding objects and links
	// invisible to this caller leaves these values unchanged.
	NodesExamined int
	LinksExamined int
}

// CallRecord is one audit entry for a HasCycle invocation.
type CallRecord struct {
	Caller   string
	HasCycle bool
	Evidence []string
	Stats    CycleStats
}

// SetAuditWriter installs a sink receiving every HasCycle record (input
// caller, output and evidence, internal metrics). Pass nil to disable.
func (g *Graph) SetAuditWriter(w func(CallRecord)) {
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	g.auditWriter = w
}

// AuditLog returns a copy of retained HasCycle records.
func (g *Graph) AuditLog() []CallRecord {
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	out := make([]CallRecord, len(g.auditLog))
	copy(out, g.auditLog)
	return out
}

func (g *Graph) record(rec CallRecord) {
	g.auditMu.Lock()
	if g.auditWriter != nil {
		g.auditWriter(rec)
	}
	g.auditLog = append(g.auditLog, rec)
	g.auditMu.Unlock()
}

// HasCycle reports whether the caller's effective subgraph (existence filter,
// then traversal filter) contains a directed cycle. Self loops and
// bidirectional round trips count. The result, including the evidence set,
// is deterministic for a given graph and permission state: it does not
// depend on a start object, traversal order, object creation order or link
// insertion order.
func (g *Graph) HasCycle(caller string) (CycleResult, error) {
	if !validCallerID(caller) {
		return CycleResult{}, ErrInvalidCaller
	}
	// A single read-lock acquisition makes the whole call observe one
	// immutable point of the serial order: concurrent mutations and
	// permission changes cannot interleave with the snapshot.
	g.mu.RLock()
	v := g.callers[caller]
	var snap snapshot
	if v != nil {
		snap = g.buildSnapshotLocked(v)
	} else {
		snap = snapshot{nodes: map[string]struct{}{}, arcs: map[string][]arc{}}
	}
	g.mu.RUnlock()

	res, stats := detectCycle(snap)
	g.record(CallRecord{
		Caller:   caller,
		HasCycle: res.HasCycle,
		Evidence: append([]string(nil), res.Evidence...),
		Stats:    stats,
	})
	return res, nil
}

// hasCycleWithStats is the internal entry point exposing cost metrics.
func (g *Graph) hasCycleWithStats(caller string) (CycleResult, CycleStats, error) {
	if !validCallerID(caller) {
		return CycleResult{}, CycleStats{}, ErrInvalidCaller
	}
	g.mu.RLock()
	v := g.callers[caller]
	var snap snapshot
	if v != nil {
		snap = g.buildSnapshotLocked(v)
	} else {
		snap = snapshot{nodes: map[string]struct{}{}, arcs: map[string][]arc{}}
	}
	g.mu.RUnlock()

	res, stats := detectCycle(snap)
	return res, stats, nil
}

// detectCycle finds the canonical cycle: among all directed simple cycles it
// selects the shortest one, breaking ties by the lexicographically smallest
// ordered vertex sequence; the returned evidence is that sequence. Starting
// nodes and arcs are processed in sorted order, which makes the answer
// independent of any iteration order or history.
func detectCycle(snap snapshot) (CycleResult, CycleStats) {
	stats := CycleStats{
		VisibleObjects: snap.nodesExamined,
		ActiveLinks:    snap.linkCount,
	}
	empty := CycleResult{Evidence: []string{}}

	nodes := make([]string, 0, len(snap.nodes))
	for id := range snap.nodes {
		nodes = append(nodes, id)
	}
	sort.Strings(nodes)

	var bestCycle []string
	for _, s := range nodes {
		cycle, nodesExamined, linksExamined := shortestCycleThrough(snap, s)
		stats.NodesExamined += nodesExamined
		stats.LinksExamined += linksExamined
		if cycle == nil {
			continue
		}
		// Canonical rule over every candidate: minimum cycle length first,
		// then lexicographically smallest ordered vertex sequence. Rotating a
		// cycle to its minimum-id vertex happens automatically because that
		// vertex is itself used as a search root.
		if bestCycle == nil ||
			len(cycle) < len(bestCycle) ||
			(len(cycle) == len(bestCycle) && lexLess(cycle, bestCycle)) {
			bestCycle = cycle
		}
	}

	if bestCycle == nil {
		return empty, stats
	}
	return CycleResult{HasCycle: true, Evidence: bestCycle}, stats
}

// shortestCycleThrough returns the lexicographically smallest shortest
// directed cycle passing through s, as the ordered vertex list
// [s, ..., s] (with s repeated at the end). s is treated only as the cycle
// root and never enqueued mid-BFS.
func shortestCycleThrough(snap snapshot, s string) ([]string, int, int) {
	const maxDist = int(^uint(0) >> 1)
	dist := map[string]int{s: maxDist}
	parent := map[string]string{}

	queue := make([]string, 0, len(snap.nodes))
	nodesExamined := 0
	linksExamined := 0

	// Seed with the sorted out-neighbours of s so shortest paths of equal
	// length are discovered in lexicographic order.
	for _, a := range snap.arcs[s] {
		linksExamined++
		if a.to == s {
			// Self loop: cycle of length one.
			return []string{s, s}, nodesExamined + 1, linksExamined
		}
		if _, seen := dist[a.to]; !seen {
			dist[a.to] = 1
			parent[a.to] = s
			queue = append(queue, a.to)
		}
	}

	var best []string
	for head := 0; head < len(queue); head++ {
		u := queue[head]
		nodesExamined++
		du := dist[u]
		if best != nil && du+1 > len(best)-1 {
			continue
		}
		for _, a := range snap.arcs[u] {
			linksExamined++
			if a.to == s {
				cycle := reconstructCycle(parent, u, s)
				if best == nil || len(cycle) < len(best) ||
					(len(cycle) == len(best) && lexLess(cycle, best)) {
					best = cycle
				}
				continue
			}
			if _, seen := dist[a.to]; !seen {
				dist[a.to] = du + 1
				parent[a.to] = u
				queue = append(queue, a.to)
			}
		}
	}
	return best, nodesExamined + 1, linksExamined
}

// reconstructCycle rebuilds [s, ..., u, s] from parent pointers.
func reconstructCycle(parent map[string]string, u, s string) []string {
	path := []string{u}
	for path[len(path)-1] != s {
		path = append(path, parent[path[len(path)-1]])
	}
	// path currently ends at s; reverse the prefix before s.
	reversed := []string{}
	for i := len(path) - 1; i >= 0; i-- {
		reversed = append(reversed, path[i])
	}
	reversed = append(reversed, s)
	return reversed
}

// lexLess compares ordered vertex sequences.
func lexLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
