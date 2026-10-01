package buildgraph

import "sort"

// NaiveDirtySet evaluates dirtiness exactly per the definition, with no
// memoization: self-dirtiness plus the dirtiness of the producing edge of any
// explicit or implicit input, evaluated independently for every edge on every
// visit. It shares the dependency closure and missing-source rules of
// DirtySet and exists for differential testing. The returned map is id ->
// reason trace ("" when clean).
func (g *Graph) NaiveDirtySet(targets []string) (map[string]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.naiveDirtySetLocked(targets)
}

func (g *Graph) naiveDirtySetLocked(targets []string) (map[string]string, error) {
	for _, p := range targets {
		if _, isOut := g.producer[p]; !isOut && len(g.users[p]) == 0 {
			return nil, &TargetError{Path: p}
		}
	}

	closure := make(map[string]*edge)
	var walk func(id string)
	walk = func(id string) {
		if _, ok := closure[id]; ok {
			return
		}
		e := g.edges[id]
		closure[id] = e
		for _, list := range [][]string{e.explicit, e.implicit, e.orderOnly} {
			for _, p := range list {
				if pid, ok := g.producer[p]; ok {
					walk(pid)
				}
			}
		}
	}
	for _, p := range targets {
		if id, ok := g.producer[p]; ok {
			walk(id)
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
				if _, produced := g.producer[p]; !produced {
					if _, exists := g.mtimes[p]; !exists {
						return nil, &MissingSourceError{EdgeID: id, Path: p}
					}
				}
			}
		}
	}

	var eval func(e *edge) bool
	eval = func(e *edge) bool {
		if g.selfDirtyReason(e) != "" {
			return true
		}
		for _, list := range [][]string{e.explicit, e.implicit} {
			for _, p := range list {
				if pid, ok := g.producer[p]; ok && eval(g.edges[pid]) {
					return true
				}
			}
		}
		return false
	}

	reasons := make(map[string]string, len(closure))
	for _, id := range ids {
		if eval(closure[id]) {
			reasons[id] = g.selfDirtyReason(closure[id])
			if reasons[id] == "" {
				reasons[id] = "upstream explicit/implicit edge dirty"
			}
		} else {
			reasons[id] = ""
		}
	}
	return reasons, nil
}
