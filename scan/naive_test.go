package scan

// 朴素参考模型：严格按题面规则逐行实现，独立于引擎代码。

type naiveRow struct{ k, v int64 }

type naiveOp struct {
	kind     string // begin/report/end/approve
	part     int
	epoch    int
	rows     []naiveRow
	complete bool
	role     int
	now      int64
}

type naiveEnd struct {
	deleted int
	tripped bool
}

type naiveModel struct {
	p, k, x int
	maxNow  int64
	t       map[int64]int64
	a       map[int64]int
	suspect map[int]bool
	open    map[int]bool
	seen    map[int]map[int64]bool
	epoch   map[int]int
}

func newNaive(p, k, x int) *naiveModel {
	m := &naiveModel{p: p, k: k, x: x,
		t: map[int64]int64{}, a: map[int64]int{},
		suspect: map[int]bool{}, open: map[int]bool{},
		seen: map[int]map[int64]bool{}, epoch: map[int]int{}}
	return m
}

func (m *naiveModel) Begin(o naiveOp) (int, error) {
	if o.part < 0 || o.part >= m.p || o.now < 0 || o.now > 1e12 {
		return 0, ErrInvalidParam
	}
	if o.now < m.maxNow {
		return 0, ErrClockRollback
	}
	if m.open[o.part] {
		return 0, ErrBusy
	}
	m.maxNow = o.now
	m.epoch[o.part]++
	ep := m.epoch[o.part]
	m.open[o.part] = true
	m.seen[o.part] = map[int64]bool{}
	return ep, nil
}

func (m *naiveModel) Report(o naiveOp) ([]WriteOutcome, error) {
	if o.part < 0 || o.part >= m.p || o.now < 0 || o.now > 1e12 {
		return nil, ErrInvalidParam
	}
	dup := map[int64]bool{}
	for _, r := range o.rows {
		if r.k < 1 || r.k > 1e9 || r.v < -1e9 || r.v > 1e9 {
			return nil, ErrInvalidParam
		}
		if int(r.k%int64(m.p)) != o.part {
			return nil, ErrInvalidParam
		}
		if dup[r.k] {
			return nil, ErrInvalidParam
		}
		dup[r.k] = true
	}
	if o.now < m.maxNow {
		return nil, ErrClockRollback
	}
	if !m.open[o.part] || m.epoch[o.part] != o.epoch {
		return nil, ErrNoSession
	}
	m.maxNow = o.now
	out := make([]WriteOutcome, len(o.rows))
	for i, r := range o.rows {
		old, exists := m.t[r.k]
		switch {
		case !exists:
			out[i] = Insert
		case old != r.v:
			out[i] = Update
		default:
			out[i] = Same
		}
		m.t[r.k] = r.v
		m.a[r.k] = 0
		m.seen[o.part][r.k] = true
	}
	return out, nil
}

func (m *naiveModel) End(o naiveOp) (naiveEnd, error) {
	if o.part < 0 || o.part >= m.p || o.now < 0 || o.now > 1e12 {
		return naiveEnd{}, ErrInvalidParam
	}
	if o.now < m.maxNow {
		return naiveEnd{}, ErrClockRollback
	}
	if !m.open[o.part] || m.epoch[o.part] != o.epoch {
		return naiveEnd{}, ErrNoSession
	}
	m.maxNow = o.now
	seen := m.seen[o.part]
	m.open[o.part] = false
	m.seen[o.part] = nil
	if !o.complete {
		return naiveEnd{}, nil
	}
	n0 := 0
	var cand []int64
	for k := range m.t {
		if int(k%int64(m.p)) != o.part {
			continue
		}
		n0++
		if !seen[k] {
			m.a[k]++
		}
	}
	for k := range m.t {
		if int(k%int64(m.p)) == o.part && m.a[k] >= m.k {
			cand = append(cand, k)
		}
	}
	c := len(cand)
	if m.suspect[o.part] {
		return naiveEnd{}, nil
	}
	if int64(c)*100 > int64(m.x)*int64(n0) {
		m.suspect[o.part] = true
		return naiveEnd{tripped: true}, nil
	}
	for _, k := range cand {
		delete(m.t, k)
		delete(m.a, k)
	}
	return naiveEnd{deleted: c}, nil
}

func (m *naiveModel) Approve(o naiveOp) (int, error) {
	if o.part < 0 || o.part >= m.p || o.now < 0 || o.now > 1e12 {
		return 0, ErrInvalidParam
	}
	if o.role != 2 {
		return 0, ErrUnauthorized
	}
	if o.now < m.maxNow {
		return 0, ErrClockRollback
	}
	if !m.suspect[o.part] {
		return 0, ErrNotSuspect
	}
	m.maxNow = o.now
	m.suspect[o.part] = false
	deleted := 0
	for k := range m.t {
		if int(k%int64(m.p)) == o.part && m.a[k] >= m.k {
			delete(m.t, k)
			delete(m.a, k)
			deleted++
		}
	}
	return deleted, nil
}
