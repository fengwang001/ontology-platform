package failover

// This file contains a deliberately literal transcription of the
// specification (the "naive" reference model). It is intentionally written
// independently from allocator.go and used by the randomized differential test
// to cross-check every visible result and the internal memory values.

type naiveHost struct {
	level   int
	healthy bool
}

type naiveModel struct {
	l     int
	f     int
	delta int
	caps  []int
	hosts map[string]naiveHost
	cur   []int
	set   []bool
}

func newNaiveModel(l, f, d int, caps []int) *naiveModel {
	return &naiveModel{
		l:     l,
		f:     f,
		delta: d,
		caps:  append([]int(nil), caps...),
		hosts: make(map[string]naiveHost),
		cur:   make([]int, l),
		set:   make([]bool, l),
	}
}

func (m *naiveModel) add(level int, id string, healthy bool) error {
	if level < 0 || level >= m.l {
		return ErrLevelOutOfRange
	}
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := m.hosts[id]; ok {
		return ErrHostExists
	}
	m.hosts[id] = naiveHost{level: level, healthy: healthy}
	return nil
}

func (m *naiveModel) remove(id string) error {
	if _, ok := m.hosts[id]; !ok {
		return ErrHostNotFound
	}
	delete(m.hosts, id)
	return nil
}

func (m *naiveModel) setHealth(id string, healthy bool) error {
	h, ok := m.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	h.healthy = healthy
	m.hosts[id] = h
	return nil
}

func (m *naiveModel) raw() ([]int, []int) {
	total := make([]int, m.l)
	healthy := make([]int, m.l)
	for _, h := range m.hosts {
		total[h.level]++
		if h.healthy {
			healthy[h.level]++
		}
	}
	raw := make([]int, m.l)
	for p := 0; p < m.l; p++ {
		if total[p] == 0 {
			raw[p] = 0
			continue
		}
		v := healthy[p] * m.f / total[p]
		if v > 100 {
			v = 100
		}
		raw[p] = v
	}
	return total, raw
}

// loads runs the full rule; on rejection memory and hosts are untouched.
func (m *naiveModel) loads() ([]int, error) {
	if len(m.hosts) == 0 {
		return nil, ErrNoHosts
	}
	total, raw := m.raw()

	// Memory update (computed against the old cur).
	next := make([]int, m.l)
	for p := 0; p < m.l; p++ {
		if !m.set[p] || m.cur[p] == 0 || raw[p] <= m.cur[p] || raw[p] >= m.cur[p]+m.delta {
			next[p] = raw[p]
		} else {
			next[p] = m.cur[p]
		}
	}

	s := 0
	for _, v := range next {
		s += v
	}
	loads := make([]int, m.l)
	eligible := make([]bool, m.l)

	switch {
	case s >= 100:
		rem := 100
		for p := 0; p < m.l; p++ {
			eligible[p] = next[p] > 0
			give := next[p]
			if give > rem {
				give = rem
			}
			loads[p] = give
			rem -= give
		}
	case s >= 1:
		rem := 100
		first := -1
		for p := 0; p < m.l; p++ {
			eligible[p] = next[p] > 0
			loads[p] = next[p] * 100 / s
			rem -= loads[p]
			if next[p] > 0 && first == -1 {
				first = p
			}
		}
		loads[first] += rem
	default:
		for p := 0; p < m.l; p++ {
			eligible[p] = total[p] > 0
		}
		for p := 0; p < m.l; p++ {
			if total[p] > 0 {
				loads[p] = 100
				break
			}
		}
	}

	x := 0
	for p := 0; p < m.l; p++ {
		if loads[p] > m.caps[p] {
			x += loads[p] - m.caps[p]
			loads[p] = m.caps[p]
		}
	}
	for p := 0; p < m.l && x > 0; p++ {
		if !eligible[p] {
			continue
		}
		room := m.caps[p] - loads[p]
		if room > x {
			room = x
		}
		loads[p] += room
		x -= room
	}
	if x > 0 {
		return nil, ErrInsufficientCapacity
	}

	m.cur = next
	for p := range m.set {
		m.set[p] = true
	}
	return loads, nil
}

func (m *naiveModel) pick(r int) (int, error) {
	if r < 0 || r > 99 {
		return 0, ErrInvalidPoint
	}
	loads, err := m.loads()
	if err != nil {
		return 0, err
	}
	prefix := 0
	for p, share := range loads {
		if r < prefix+share {
			return p, nil
		}
		prefix += share
	}
	return 0, ErrInsufficientCapacity
}
