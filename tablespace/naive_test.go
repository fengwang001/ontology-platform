package tablespace

// naiveModel is a deliberately simple re-implementation of the documented
// rules, used as an oracle in randomized differential tests. It tracks page
// ownership directly and scans extents/pages in ascending order, exactly as a
// step-by-step reading of the specification would.
type naiveModel struct {
	x, f, e   int
	state     []string // FREE, FRAG, FULLFRAG, SEG
	owner     []int    // segment state owner (0 unless SEG)
	used      []int    // allocated pages per extent
	pageOwner []int    // 0 = free
	alive     map[int]bool
	segUsed   map[int]int
	queue     map[int][]int // non-full exclusive extents in FIFO order
	exclusive map[int]map[int]bool
	nextID    int
}

func newNaive(x, f, e int) *naiveModel {
	m := &naiveModel{
		x:         x,
		f:         f,
		e:         e,
		state:     make([]string, e),
		owner:     make([]int, e),
		used:      make([]int, e),
		pageOwner: make([]int, e*x),
		alive:     map[int]bool{},
		segUsed:   map[int]int{},
		queue:     map[int][]int{},
		exclusive: map[int]map[int]bool{},
		nextID:    1,
	}
	for i := range m.state {
		m.state[i] = "FREE"
	}
	return m
}

func (m *naiveModel) newSegment() int {
	id := m.nextID
	m.nextID++
	m.alive[id] = true
	m.segUsed[id] = 0
	m.queue[id] = []int{}
	m.exclusive[id] = map[int]bool{}
	return id
}

func (m *naiveModel) inQueue(s, eid int) bool {
	for _, v := range m.queue[s] {
		if v == eid {
			return true
		}
	}
	return false
}

func (m *naiveModel) enqueue(s, eid int) {
	if !m.inQueue(s, eid) {
		m.queue[s] = append(m.queue[s], eid)
	}
}

func (m *naiveModel) dequeue(s, eid int) {
	q := m.queue[s]
	for i, v := range q {
		if v == eid {
			m.queue[s] = append(q[:i], q[i+1:]...)
			return
		}
	}
}

// result mirrors (page, errKind); errKind is "", "invalid", "noseg",
// "notowned", "nospace".
func (m *naiveModel) alloc(s, hint int) (int, string) {
	if hint < -1 || hint >= m.e*m.x {
		return 0, "invalid"
	}
	if !m.alive[s] {
		return 0, "noseg"
	}

	if m.segUsed[s] < m.f {
		// smallest FRAG extent
		eid := -1
		for i := 0; i < m.e; i++ {
			if m.state[i] == "FRAG" {
				eid = i
				break
			}
		}
		if eid < 0 {
			for i := 0; i < m.e; i++ {
				if m.state[i] == "FREE" {
					eid = i
					break
				}
			}
			if eid < 0 {
				return 0, "nospace"
			}
			m.state[eid] = "FRAG"
		}
		p := m.minFreePage(eid)
		m.pageOwner[p] = s
		m.used[eid]++
		m.segUsed[s]++
		if m.used[eid] == m.x {
			m.state[eid] = "FULLFRAG"
		}
		return p, ""
	}

	// exclusive mode
	if hint >= 0 {
		he := hint / m.x
		if m.state[he] == "SEG" && m.owner[he] == s && m.pageOwner[hint] == 0 {
			m.pageOwner[hint] = s
			m.used[he]++
			m.segUsed[s]++
			if m.used[he] == m.x {
				m.dequeue(s, he)
			}
			return hint, ""
		}
	}

	if len(m.queue[s]) > 0 {
		eid := m.queue[s][0]
		p := m.minFreePage(eid)
		m.pageOwner[p] = s
		m.used[eid]++
		m.segUsed[s]++
		if m.used[eid] == m.x {
			m.dequeue(s, eid)
		}
		return p, ""
	}

	eid := -1
	for i := 0; i < m.e; i++ {
		if m.state[i] == "FREE" {
			eid = i
			break
		}
	}
	if eid < 0 {
		return 0, "nospace"
	}
	m.state[eid] = "SEG"
	m.owner[eid] = s
	m.exclusive[s][eid] = true
	p := m.minFreePage(eid)
	m.pageOwner[p] = s
	m.used[eid] = 1
	m.segUsed[s] = 1 + m.segUsed[s]
	m.enqueue(s, eid)
	return p, ""
}

func (m *naiveModel) minFreePage(eid int) int {
	for off := 0; off < m.x; off++ {
		p := eid*m.x + off
		if m.pageOwner[p] == 0 {
			return p
		}
	}
	panic("naive: no free page in extent")
}

func (m *naiveModel) free(s, p int) string {
	if p < 0 || p >= m.e*m.x {
		return "invalid"
	}
	if !m.alive[s] {
		return "noseg"
	}
	if m.pageOwner[p] != s {
		return "notowned"
	}
	eid := p / m.x
	m.pageOwner[p] = 0
	m.used[eid]--
	m.segUsed[s]--

	switch m.state[eid] {
	case "FULLFRAG":
		m.state[eid] = "FRAG"
		if m.used[eid] == 0 {
			m.state[eid] = "FREE"
		}
	case "FRAG":
		if m.used[eid] == 0 {
			m.state[eid] = "FREE"
		}
	case "SEG":
		wasFull := m.used[eid]+1 == m.x
		if m.used[eid] == 0 {
			m.dequeue(s, eid)
			m.state[eid] = "FREE"
			m.owner[eid] = 0
			delete(m.exclusive[s], eid)
		} else if wasFull {
			m.enqueue(s, eid)
		}
	}
	return ""
}

func (m *naiveModel) freeSegment(s int) string {
	if !m.alive[s] {
		return "noseg"
	}
	// Fragment pages. Iterate pages in ascending order; final state is
	// order-independent under the rules.
	for p := 0; p < m.e*m.x; p++ {
		st := m.state[p/m.x]
		if m.pageOwner[p] == s && (st == "FRAG" || st == "FULLFRAG") {
			if ek := m.free(s, p); ek != "" {
				panic("naive: frag free failed: " + ek)
			}
		}
	}
	// Exclusive extents.
	var exts []int
	for eid := range m.exclusive[s] {
		exts = append(exts, eid)
	}
	// Release pages ascending within each extent.
	for _, eid := range exts {
		for off := 0; off < m.x; off++ {
			p := eid*m.x + off
			if m.pageOwner[p] == s {
				if ek := m.free(s, p); ek != "" {
					panic("naive: seg free failed: " + ek)
				}
			}
		}
	}
	m.alive[s] = false
	delete(m.segUsed, s)
	delete(m.queue, s)
	delete(m.exclusive, s)
	return ""
}
