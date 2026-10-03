package topo

// A naive reference model of the specification, written as a direct
// step-by-step transcription of the rules. The randomized test cross-checks
// the real Maintainer against this model on every operation.

import "sort"

type model struct {
	N, E   int
	nextID int
	alive  map[int]bool
	ord    map[int]int
	out    map[int]map[int]bool
	in     map[int]map[int]bool
	nedge  int
	touch  int64
}

func newModel(n, e int) *model {
	return &model{
		N: n, E: e,
		alive: make(map[int]bool),
		ord:   make(map[int]int),
		out:   make(map[int]map[int]bool),
		in:    make(map[int]map[int]bool),
	}
}

func (mo *model) addNode() (int, error) {
	if mo.nextID >= mo.N {
		return 0, ErrNodeLimit
	}
	id := mo.nextID
	mo.nextID++
	mo.alive[id] = true
	mo.ord[id] = id
	mo.out[id] = make(map[int]bool)
	mo.in[id] = make(map[int]bool)
	return id, nil
}

func (mo *model) addEdge(u, v int) (EdgeResult, error) {
	if !mo.alive[u] || !mo.alive[v] {
		return EdgeResult{}, ErrNodeNotFound
	}
	if u == v {
		return EdgeResult{}, &CycleError{Path: []int{u}}
	}
	if mo.out[u][v] {
		return EdgeResult{}, ErrEdgeExists
	}
	if mo.nedge >= mo.E {
		return EdgeResult{}, ErrEdgeLimit
	}
	if mo.ord[u] < mo.ord[v] {
		mo.link(u, v)
		return EdgeResult{}, nil
	}
	lb, ub := mo.ord[v], mo.ord[u]
	// deltaF: BFS from v over out-edges, keeping ord <= ub.
	dF := map[int]bool{v: true}
	for queue := []int{v}; len(queue) > 0; queue = queue[1:] {
		for w := range mo.out[queue[0]] {
			if !dF[w] && mo.ord[w] <= ub {
				dF[w] = true
				queue = append(queue, w)
			}
		}
	}
	if dF[u] {
		return EdgeResult{}, &CycleError{Path: mo.witness(v, u)}
	}
	// deltaB: BFS from u over in-edges, keeping ord >= lb.
	dB := map[int]bool{u: true}
	for queue := []int{u}; len(queue) > 0; queue = queue[1:] {
		for w := range mo.in[queue[0]] {
			if !dB[w] && mo.ord[w] >= lb {
				dB[w] = true
				queue = append(queue, w)
			}
		}
	}
	mo.touch += int64(len(dF) + len(dB))
	byOrd := func(set map[int]bool) []int {
		s := make([]int, 0, len(set))
		for x := range set {
			s = append(s, x)
		}
		sort.Slice(s, func(i, j int) bool { return mo.ord[s[i]] < mo.ord[s[j]] })
		return s
	}
	seq := append(byOrd(dB), byOrd(dF)...)
	pool := make([]int, 0, len(seq))
	for _, x := range seq {
		pool = append(pool, mo.ord[x])
	}
	sort.Ints(pool)
	var moved []int
	for i, x := range seq {
		if mo.ord[x] != pool[i] {
			mo.ord[x] = pool[i]
			moved = append(moved, x)
		}
	}
	sort.Slice(moved, func(i, j int) bool { return mo.ord[moved[i]] < mo.ord[moved[j]] })
	mo.link(u, v)
	return EdgeResult{Moved: moved, DeltaF: len(dF), DeltaB: len(dB)}, nil
}

func (mo *model) witness(v, u int) []int {
	dist := map[int]int{u: 0}
	for queue := []int{u}; len(queue) > 0; queue = queue[1:] {
		x := queue[0]
		for w := range mo.in[x] {
			if _, ok := dist[w]; !ok {
				dist[w] = dist[x] + 1
				queue = append(queue, w)
			}
		}
	}
	d := dist[v]
	path := []int{v}
	for step := 1; step <= d; step++ {
		cur := path[len(path)-1]
		best := -1
		for w := range mo.out[cur] {
			if dw, ok := dist[w]; ok && dw == d-step && (best < 0 || w < best) {
				best = w
			}
		}
		path = append(path, best)
	}
	return path
}

func (mo *model) link(u, v int) {
	mo.out[u][v] = true
	mo.in[v][u] = true
	mo.nedge++
}

func (mo *model) removeEdge(u, v int) error {
	if !mo.alive[u] || !mo.alive[v] {
		return ErrNodeNotFound
	}
	if !mo.out[u][v] {
		return ErrEdgeNotFound
	}
	delete(mo.out[u], v)
	delete(mo.in[v], u)
	mo.nedge--
	return nil
}

func (mo *model) removeNode(x int) error {
	if !mo.alive[x] {
		return ErrNodeNotFound
	}
	for w := range mo.out[x] {
		delete(mo.in[w], x)
		mo.nedge--
	}
	for w := range mo.in[x] {
		delete(mo.out[w], x)
		mo.nedge--
	}
	delete(mo.alive, x)
	delete(mo.ord, x)
	delete(mo.out, x)
	delete(mo.in, x)
	return nil
}

func (mo *model) addEdges(edges [][2]int) (BatchResult, error) {
	if len(edges) < 1 || len(edges) > 1000 {
		return BatchResult{}, ErrBatchSize
	}
	savedOrd := make(map[int]int, len(mo.ord))
	for x, o := range mo.ord {
		savedOrd[x] = o
	}
	savedOut := make(map[int]map[int]bool, len(mo.out))
	savedIn := make(map[int]map[int]bool, len(mo.in))
	for x, s := range mo.out {
		c := make(map[int]bool, len(s))
		for w := range s {
			c[w] = true
		}
		savedOut[x] = c
	}
	for x, s := range mo.in {
		c := make(map[int]bool, len(s))
		for w := range s {
			c[w] = true
		}
		savedIn[x] = c
	}
	savedNedge, savedTouch := mo.nedge, mo.touch

	res := BatchResult{Counts: make([][2]int, len(edges))}
	for i, e := range edges {
		r, err := mo.addEdge(e[0], e[1])
		if err != nil {
			mo.ord, mo.out, mo.in = savedOrd, savedOut, savedIn
			mo.nedge, mo.touch = savedNedge, savedTouch
			return BatchResult{}, &BatchError{Index: i, Reason: err}
		}
		res.Counts[i] = [2]int{r.DeltaF, r.DeltaB}
	}
	for x, o := range mo.ord {
		if savedOrd[x] != o {
			res.Moved = append(res.Moved, x)
		}
	}
	sort.Slice(res.Moved, func(i, j int) bool { return mo.ord[res.Moved[i]] < mo.ord[res.Moved[j]] })
	return res, nil
}

func (mo *model) order() []int {
	nodes := make([]int, 0, len(mo.alive))
	for x := range mo.alive {
		nodes = append(nodes, x)
	}
	sort.Slice(nodes, func(i, j int) bool { return mo.ord[nodes[i]] < mo.ord[nodes[j]] })
	return nodes
}
