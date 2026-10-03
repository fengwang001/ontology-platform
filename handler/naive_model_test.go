package handler

import "ontology/history"

type naiveModel struct {
	cap    int64
	k      int
	s      int64
	p      int64
	closed bool
	queue  []naivePending
	dedup  []naiveEntry // 头最旧
	log    []history.Event
}

type naivePending struct {
	uid   string
	delta int64
	seq   int64 // U 事件序号
}

type naiveEntry struct {
	uid string
	r   Result
}

func newNaive(cap int64, k int) *naiveModel {
	return &naiveModel{cap: cap, k: k}
}

func (m *naiveModel) lookup(uid string) (Result, bool) {
	for i := range m.dedup {
		if m.dedup[i].uid == uid {
			return m.dedup[i].r, true
		}
	}
	return Result{}, false
}

func (m *naiveModel) put(uid string, r Result) {
	for i := range m.dedup {
		if m.dedup[i].uid == uid {
			m.dedup[i].r = r
			e := m.dedup[i]
			copy(m.dedup[i:], m.dedup[i+1:])
			m.dedup[len(m.dedup)-1] = e
			return
		}
	}
	m.dedup = append(m.dedup, naiveEntry{uid, r})
	if len(m.dedup) > m.k {
		m.dedup = m.dedup[1:]
	}
}

func (m *naiveModel) update(uid string, delta int64) (Result, error) {
	if r, ok := m.lookup(uid); ok {
		return r, nil
	}
	if m.closed {
		return Result{}, ErrClosed
	}
	if m.p+delta < 0 || m.p+delta > m.cap {
		r := Result{Kind: Rejected}
		m.put(uid, r)
		return r, nil
	}
	seq := int64(len(m.log)) + 1
	m.log = append(m.log, history.Event{Index: seq, Type: history.TypeUpdate,
		UID: []byte(uid), Delta: delta})
	m.p += delta
	m.queue = append(m.queue, naivePending{uid, delta, seq})
	r := Result{Kind: Accepted, Seq: seq}
	m.put(uid, r)
	return r, nil
}

func (m *naiveModel) step() error {
	if len(m.queue) == 0 {
		return ErrEmpty
	}
	head := m.queue[0]
	m.queue = m.queue[1:]
	m.s += head.delta
	seq := int64(len(m.log)) + 1
	m.log = append(m.log, history.Event{Index: seq, Type: history.TypeApplied, Seq: head.seq})
	m.put(head.uid, Result{Kind: Completed, Val: m.s})
	return nil
}

func (m *naiveModel) close() {
	if m.closed {
		return
	}
	m.closed = true
	seq := int64(len(m.log)) + 1
	m.log = append(m.log, history.Event{Index: seq, Type: history.TypeClosed})
	for _, q := range m.queue {
		m.put(q.uid, Result{Kind: Aborted})
	}
	m.queue = nil
}

// rebuild 丢弃内存，只凭 log 重建朴素模型（等价于 Recover）。
func (m *naiveModel) rebuild(k int) *naiveModel {
	n := &naiveModel{cap: m.cap, k: k}
	var ups []naivePending
	applied := 0
	for _, e := range m.log {
		switch e.Type {
		case history.TypeUpdate:
			ups = append(ups, naivePending{string(e.UID), e.Delta, e.Index})
		case history.TypeApplied:
			applied++
		case history.TypeClosed:
			n.closed = true
		}
	}
	if applied > len(ups) {
		applied = len(ups)
	}
	var s int64
	for i, u := range ups {
		if i < applied {
			s += u.delta
			n.put(u.uid, Result{Kind: Completed, Val: s})
			continue
		}
		if n.closed {
			n.put(u.uid, Result{Kind: Aborted})
		} else {
			n.queue = append(n.queue, u)
			n.put(u.uid, Result{Kind: Accepted, Seq: u.seq})
		}
	}
	n.s = s
	if n.closed {
		n.p = s
	} else {
		n.p = s
		for _, q := range n.queue {
			n.p += q.delta
		}
	}
	n.log = m.log
	return n
}
