package ontology

import "fmt"

// naiveAllocator is an independent, step-by-step transcription of the
// specification, used as the oracle for randomized replay tests.
type naiveAllocator struct {
	L, F, D int
	cap     []int
	hosts   map[string]*host
	cur     []int
	curNil  []bool
}

func newNaive(L, F, d int, caps []int) *naiveAllocator {
	cp := make([]int, L)
	copy(cp, caps)
	return &naiveAllocator{
		L: L, F: F, D: d, cap: cp,
		hosts:  make(map[string]*host),
		cur:    make([]int, L),
		curNil: make([]bool, L),
	}
}

func (n *naiveAllocator) raw(p int) int {
	t := 0
	h := 0
	for _, x := range n.hosts {
		if x.level == p {
			t++
			if x.healthy {
				h++
			}
		}
	}
	if t == 0 {
		return 0
	}
	v := h * n.F / t
	if v > 100 {
		return 100
	}
	return v
}

func (n *naiveAllocator) add(level int, id string, healthy bool) error {
	if level < 0 || level >= n.L {
		return ErrLevelOutOfRange
	}
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := n.hosts[id]; ok {
		return ErrDuplicateID
	}
	n.hosts[id] = &host{level: level, healthy: healthy}
	return nil
}

func (n *naiveAllocator) remove(id string) error {
	if _, ok := n.hosts[id]; !ok {
		return ErrHostNotFound
	}
	delete(n.hosts, id)
	return nil
}

func (n *naiveAllocator) setHealth(id string, healthy bool) error {
	x, ok := n.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	x.healthy = healthy
	return nil
}

// loads computes the shares. Returns the shares, an error, the branch
// taken and the redistributed excess X, for logging.
func (n *naiveAllocator) loads(commit bool) ([]int, error, string, int) {
	if len(n.hosts) == 0 {
		return nil, ErrNoHosts, "no-hosts", 0
	}
	raw := make([]int, n.L)
	memorized := make([]int, n.L)
	for p := 0; p < n.L; p++ {
		raw[p] = n.raw(p)
		c := 0
		if !n.curNil[p] {
			c = n.cur[p]
		}
		switch {
		case n.curNil[p] || c == 0 || raw[p] <= c || raw[p] >= c+n.D:
			memorized[p] = raw[p]
		default:
			memorized[p] = c
		}
	}
	s := 0
	for _, v := range memorized {
		s += v
	}
	out := make([]int, n.L)
	branch := ""
	panicMode := false
	switch {
	case s == 0:
		panicMode = true
		branch = "panic"
		for p := 0; p < n.L; p++ {
			if n.levelHas(p) {
				out[p] = 100
				break
			}
		}
	case s >= 100:
		branch = "greedy"
		rest := 100
		for p := 0; p < n.L; p++ {
			give := memorized[p]
			if give > rest {
				give = rest
			}
			out[p] = give
			rest -= give
		}
	default:
		branch = fmt.Sprintf("scale(S=%d)", s)
		given := 0
		for p := 0; p < n.L; p++ {
			out[p] = memorized[p] * 100 / s
			given += out[p]
		}
		for p := 0; p < n.L; p++ {
			if memorized[p] > 0 {
				out[p] += 100 - given
				break
			}
		}
	}
	excess := 0
	for p := 0; p < n.L; p++ {
		if out[p] > n.cap[p] {
			excess += out[p] - n.cap[p]
			out[p] = n.cap[p]
		}
	}
	for p := 0; p < n.L && excess > 0; p++ {
		ok := memorized[p] > 0
		if panicMode {
			ok = n.levelHas(p)
		}
		if !ok {
			continue
		}
		room := n.cap[p] - out[p]
		if room <= 0 {
			continue
		}
		give := excess
		if give > room {
			give = room
		}
		out[p] += give
		excess -= give
	}
	if excess > 0 {
		return nil, ErrOutOfCapacity, branch + "/insufficient-capacity", excess
	}
	if commit {
		for p := 0; p < n.L; p++ {
			n.cur[p] = memorized[p]
			n.curNil[p] = false
		}
	}
	return out, nil, branch, excess
}

func (n *naiveAllocator) levelHas(p int) bool {
	for _, x := range n.hosts {
		if x.level == p {
			return true
		}
	}
	return false
}

func (n *naiveAllocator) pick(r int, commit bool) (int, error, string, int) {
	if r < 0 || r > 99 {
		return 0, ErrInvalidPoint, "bad-point", 0
	}
	out, err, branch, x := n.loads(commit)
	if err != nil {
		return 0, err, branch, x
	}
	prefix := 0
	for p, v := range out {
		if r < prefix+v {
			return p, nil, branch, x
		}
		prefix += v
	}
	return 0, ErrOutOfCapacity, branch, x
}
