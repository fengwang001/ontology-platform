// Package wfg maintains the task wait-for graph: an edge task -> holder means
// the task is blocked on a mutex held by holder. It depends on no other
// package. A task waits on at most one mutex, hence has out-degree <= 1.
package wfg

// Graph is a directed wait-for graph.
type Graph struct {
	next map[string]string // waiter -> holder it is blocked on
	why  map[string]string // waiter -> mutex name
}

// New returns an empty graph.
func New() *Graph { return &Graph{next: map[string]string{}, why: map[string]string{}} }

// Add records that waiter is blocked on mutex held by holder.
func (g *Graph) Add(waiter, holder, mu string) {
	g.next[waiter] = holder
	g.why[waiter] = mu
}

// Remove deletes the outgoing edge of waiter (granted / timeout / abort).
func (g *Graph) Remove(waiter string) {
	delete(g.next, waiter)
	delete(g.why, waiter)
}

// Why reports the mutex a task waits on ("" if none).
func (g *Graph) Why(waiter string) string { return g.why[waiter] }

// WaitingOn reports the holder a task waits on ("" if none).
func (g *Graph) WaitingOn(waiter string) string { return g.next[waiter] }

// Cycle reports the cycle that would result if requester waited on holder,
// without mutating the graph. A cycle exists iff holder (following wait edges
// holder -> the task it is blocked on) already reaches requester. The result
// starts at requester and follows the wait direction; its last element is the
// task whose wait edge closes back to requester.
func (g *Graph) Cycle(requester, holder string) []string {
	cur := holder
	trail := []string{requester}
	for cur != "" {
		if cur == requester {
			return trail
		}
		trail = append(trail, cur)
		cur = g.next[cur]
	}
	return nil
}

// Acyclic reports whether the whole graph is currently acyclic (invariant
// used by randomized tests).
func (g *Graph) Acyclic() bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string) bool
	visit = func(t string) bool {
		if color[t] == gray {
			return false
		}
		if color[t] == black {
			return true
		}
		color[t] = gray
		if h := g.next[t]; h != "" {
			if !visit(h) {
				return false
			}
		}
		color[t] = black
		return true
	}
	for t := range g.next {
		if !visit(t) {
			return false
		}
	}
	return true
}
