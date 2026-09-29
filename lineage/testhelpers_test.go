package lineage

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func runTasks(t *testing.T, tasks []func(), shuffle bool) {
	t.Helper()
	if !shuffle {
		for _, task := range tasks {
			task()
		}
		return
	}
	idx := rand.Perm(len(tasks))
	var wg sync.WaitGroup
	wg.Add(len(tasks))
	for _, i := range idx {
		go func(task func()) {
			defer wg.Done()
			task()
		}(tasks[i])
	}
	wg.Wait()
}

func dumpGraph(g *Graph) string {
	if g == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("root=%s nodes:", g.Root)
	for _, n := range g.Nodes {
		s += fmt.Sprintf("\n  %s@%s %s", n.ID, n.Version, n.Operation)
	}
	s += "\nedges:"
	for _, e := range g.Edges {
		s += fmt.Sprintf("\n  %s@%s -> %s@%s active=%v",
			e.From.ID, e.From.Version, e.To.ID, e.To.Version, e.Active)
	}
	return s
}

func graphsEqual(g1, g2 *Graph) bool {
	if g1 == nil || g2 == nil {
		return g1 == g2
	}
	if len(g1.Nodes) != len(g2.Nodes) || len(g1.Edges) != len(g2.Edges) {
		return false
	}
	for i := range g1.Nodes {
		n1, n2 := g1.Nodes[i], g2.Nodes[i]
		if n1.ID != n2.ID || n1.Version != n2.Version || n1.Kind != n2.Kind ||
			n1.Operation != n2.Operation {
			return false
		}
		if len(n1.Inputs) != len(n2.Inputs) {
			return false
		}
		for j := range n1.Inputs {
			if n1.Inputs[j] != n2.Inputs[j] {
				return false
			}
		}
	}
	for i := range g1.Edges {
		if g1.Edges[i] != g2.Edges[i] {
			return false
		}
	}
	return true
}
