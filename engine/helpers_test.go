package engine

import (
	"strconv"

	"ontology/model"
)

func tasks(g *model.Graph) []int {
	var ts []int
	for v := 1; v <= g.N; v++ {
		if g.Kinds[v] == model.Task {
			ts = append(ts, v)
		}
	}
	return ts
}

func equalTerminal(eng *Instance, nav *naive) bool {
	return eng.Status().Status == nav.status() &&
		eng.endCount == nav.endCount &&
		eng.firesEqual(nav.fires)
}

func (in *Instance) firesEqual(f []int) bool {
	for v := 1; v <= in.n; v++ {
		if in.fires[v] != f[v] {
			return false
		}
	}
	return true
}

func dump(g *model.Graph) string {
	s := "n=" + strconv.Itoa(g.N) + "\n"
	for _, e := range g.Edges {
		s += strconv.Itoa(e.U) + "->" + strconv.Itoa(e.V) + " "
	}
	return s
}
