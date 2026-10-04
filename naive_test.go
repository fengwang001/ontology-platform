package ontology_test

import "ontology/adjust"

// 朴素模型：保存全部被接受 Move 流水，逐条求和；独立镜像阶段机与审批规则。
// 不变量：book == init + Σ流水 + Σ调整，且任意时刻 book >= 0。
type naiveLoc struct {
	init, price int64
	moves       []int64 // 全部被接受的 Move（逐条保存）
	adj         []int64 // 全部被采纳/批准的调整（逐条保存）
	task        adjust.ID
	phase       adjust.Phase
	c1, c2      int64
	m1, m2      int64 // mv 快照（mv 由流水逐条求和得到）
	p1, p2, p3  adjust.ID
	pending     int64
}

func (n *naiveLoc) book() int64 {
	b := n.init
	for _, mv := range n.moves {
		b += mv
	}
	for _, a := range n.adj {
		b += a
	}
	return b
}

func (n *naiveLoc) mv() int64 {
	v := int64(0)
	for _, mv := range n.moves {
		v += mv
	}
	return v
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func containsID(xs []adjust.ID, x adjust.ID) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

type naiveTask struct {
	locs   []adjust.ID
	closed bool
}

type naive struct {
	tabs, tpct, lim int64
	locs            map[adjust.ID]*naiveLoc
	tasks           map[adjust.ID]*naiveTask
	perm            map[adjust.ID]uint8
}

func newNaive(tabs, tpct, lim int64) *naive {
	return &naive{
		tabs: tabs, tpct: tpct, lim: lim,
		locs:  map[adjust.ID]*naiveLoc{},
		tasks: map[adjust.ID]*naiveTask{},
		perm:  map[adjust.ID]uint8{},
	}
}

func validIDN(id adjust.ID) bool { return len(id) >= 1 && len(id) <= 32 }

func (m *naive) tolAt(b int64) int64 {
	v := b * m.tpct / 100
	if m.tabs > v {
		return m.tabs
	}
	return v
}

func (m *naive) addLoc(loc adjust.ID, book, price int64) error {
	if !validIDN(loc) || book < 0 || book > 1e9 || price < 0 || price > 1e6 {
		return adjust.ErrInvalid
	}
	if _, ok := m.locs[loc]; ok {
		return adjust.ErrConflict
	}
	m.locs[loc] = &naiveLoc{init: book, price: price, phase: adjust.PhaseIdle}
	return nil
}

func (m *naive) move(loc adjust.ID, d int64) error {
	if !validIDN(loc) || d == 0 || d < -1e9 || d > 1e9 {
		return adjust.ErrInvalid
	}
	l, ok := m.locs[loc]
	if !ok {
		return adjust.ErrNotFound
	}
	b := l.book()
	if b+d < 0 {
		return adjust.ErrUnderstock
	}
	if b+d > 1e9 {
		return adjust.ErrInvalid
	}
	l.moves = append(l.moves, d)
	return nil
}

func (m *naive) open(task adjust.ID, locs []adjust.ID) error {
	if !validIDN(task) || len(locs) < 1 || len(locs) > 1000 {
		return adjust.ErrInvalid
	}
	seen := map[adjust.ID]bool{}
	for _, loc := range locs {
		if !validIDN(loc) || seen[loc] {
			return adjust.ErrInvalid
		}
		seen[loc] = true
		l, ok := m.locs[loc]
		if !ok {
			return adjust.ErrNotFound
		}
		if l.task != "" {
			return adjust.ErrConflict
		}
	}
	if _, ok := m.tasks[task]; ok {
		return adjust.ErrConflict
	}
	m.tasks[task] = &naiveTask{locs: append([]adjust.ID(nil), locs...)}
	for _, loc := range locs {
		l := m.locs[loc]
		l.task = task
		l.phase = adjust.PhaseFirst
		l.c1, l.c2, l.m1, l.m2 = 0, 0, 0, 0
		l.p1, l.p2, l.p3, l.pending = "", "", "", 0
	}
	return nil
}

func (m *naive) submit(task, loc adjust.ID, counted int64, p adjust.ID) error {
	if !validIDN(task) || !validIDN(loc) || !validIDN(p) || counted < 0 || counted > 1e9 {
		return adjust.ErrInvalid
	}
	t, ok := m.tasks[task]
	if !ok {
		return adjust.ErrNotFound
	}
	l, ok := m.locs[loc]
	if !ok {
		return adjust.ErrNotFound
	}
	if !containsID(t.locs, loc) {
		return adjust.ErrNotFound
	}
	if t.closed {
		return adjust.ErrState
	}
	b := l.book()
	diff := counted - b
	switch l.phase {
	case adjust.PhaseFirst:
		l.c1, l.p1, l.m1 = counted, p, l.mv()
		if abs64(diff) <= m.tolAt(b) {
			l.adj = append(l.adj, diff)
			l.phase = adjust.PhaseDone
			return nil
		}
		l.phase = adjust.PhaseSecond
		return nil
	case adjust.PhaseSecond:
		if p == l.p1 {
			return adjust.ErrMustSwitch
		}
		l.c2, l.p2, l.m2 = counted, p, l.mv()
		if abs64(diff) <= m.tolAt(b) {
			l.adj = append(l.adj, diff)
			l.phase = adjust.PhaseDone
			return nil
		}
		if counted-l.c1 == l.mv()-l.m1 {
			l.pending = diff
			l.phase = adjust.PhasePending
			return nil
		}
		l.phase = adjust.PhaseThird
		return nil
	case adjust.PhaseThird:
		if p == l.p1 || p == l.p2 {
			return adjust.ErrMustSwitch
		}
		l.p3 = p
		if abs64(diff) <= m.tolAt(b) {
			l.adj = append(l.adj, diff)
			l.phase = adjust.PhaseDone
			return nil
		}
		l.pending = diff
		l.phase = adjust.PhasePending
		return nil
	default:
		return adjust.ErrState
	}
}

func (m *naive) checkApprove(task, loc, p adjust.ID) (*naiveLoc, error) {
	if !validIDN(task) || !validIDN(loc) || !validIDN(p) {
		return nil, adjust.ErrInvalid
	}
	t, ok := m.tasks[task]
	if !ok {
		return nil, adjust.ErrNotFound
	}
	l, ok := m.locs[loc]
	if !ok {
		return nil, adjust.ErrNotFound
	}
	if !containsID(t.locs, loc) {
		return nil, adjust.ErrNotFound
	}
	if t.closed || l.phase != adjust.PhasePending {
		return nil, adjust.ErrState
	}
	if m.perm[p]&adjust.PermApprove == 0 {
		return nil, adjust.ErrNoApprove
	}
	if p == l.p1 || p == l.p2 || p == l.p3 {
		return nil, adjust.ErrMustSwitch
	}
	if abs64(l.pending)*l.price > m.lim && m.perm[p]&adjust.PermSenior == 0 {
		return nil, adjust.ErrNoSenior
	}
	return l, nil
}

func (m *naive) approve(task, loc, p adjust.ID) error {
	l, err := m.checkApprove(task, loc, p)
	if err != nil {
		return err
	}
	if l.book()+l.pending < 0 {
		return adjust.ErrUnderstock
	}
	l.adj = append(l.adj, l.pending)
	l.phase = adjust.PhaseDone
	l.pending = 0
	return nil
}

func (m *naive) reject(task, loc, p adjust.ID) error {
	l, err := m.checkApprove(task, loc, p)
	if err != nil {
		return err
	}
	l.phase = adjust.PhaseDone
	l.pending = 0
	return nil
}

func (m *naive) grant(p adjust.ID, a, sr bool) {
	var bits uint8
	if a {
		bits |= adjust.PermApprove
	}
	if sr {
		bits |= adjust.PermSenior
	}
	m.perm[p] = bits
}

func (m *naive) close(task adjust.ID) error {
	if !validIDN(task) {
		return adjust.ErrInvalid
	}
	t, ok := m.tasks[task]
	if !ok {
		return adjust.ErrNotFound
	}
	if t.closed {
		return adjust.ErrState
	}
	for _, loc := range t.locs {
		if m.locs[loc].phase != adjust.PhaseDone {
			return adjust.ErrState
		}
	}
	t.closed = true
	for _, loc := range t.locs {
		l := m.locs[loc]
		l.task = ""
		l.phase = adjust.PhaseIdle
		l.c1, l.c2, l.m1, l.m2 = 0, 0, 0, 0
		l.p1, l.p2, l.p3, l.pending = "", "", "", 0
	}
	return nil
}
