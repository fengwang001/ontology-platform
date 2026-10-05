package cpm

func (m *Maintainer) criticalPathLocked() []int {
	path := []int{}
	start := -1
	for v := 0; v < m.taskCount; v++ {
		if !m.critical[v] {
			continue
		}
		hasCriticalDriver := false
		for _, edge := range m.inEdges[v] {
			u := edge.from
			if m.critical[u] && edge.fwd.value == m.es[v] {
				hasCriticalDriver = true
				break
			}
		}
		if !hasCriticalDriver {
			start = v
			break
		}
	}
	if start < 0 {
		return path
	}

	current := start
	visited := map[int]struct{}{}
	for current >= 0 {
		if _, repeated := visited[current]; repeated {
			break
		}
		visited[current] = struct{}{}
		path = append(path, current)

		next := -1
		for _, edge := range m.outEdges[current] {
			w := edge.to
			if m.critical[w] && edge.fwd.value == m.es[w] && (next < 0 || w < next) {
				next = w
			}
		}
		current = next
	}
	return path
}
