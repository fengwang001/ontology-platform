package replen

import (
	"slices"
	"sort"
)

// 朴素模拟器：每个判定都从全部任务全量重算 inTransit 与 avail，作为增量实现的对照基准。

type mTask struct {
	id     int64
	loc    string
	qty    int64
	urgent bool
	status int // 0 open 1 done 2 cancelled
}

type mSlot struct {
	min, max, cap, c, onHand, starved int64
}

type naive struct {
	slots   map[string]*mSlot
	reserve int64
	tasks   map[int64]*mTask
	seq     int64
}

func newNaive() *naive {
	return &naive{slots: map[string]*mSlot{}, tasks: map[int64]*mTask{}}
}

func (m *naive) open(loc string) []*mTask {
	var ids []int64
	for _, tk := range m.tasks {
		if tk.status == 0 && tk.loc == loc {
			ids = append(ids, tk.id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*mTask, len(ids))
	for i, id := range ids {
		out[i] = m.tasks[id]
	}
	return out
}

func (m *naive) transit(loc string) int64 {
	var sum int64
	for _, tk := range m.open(loc) {
		sum += tk.qty
	}
	return sum
}

func (m *naive) openQty() int64 {
	var sum int64
	for _, tk := range m.tasks {
		if tk.status == 0 {
			sum += tk.qty
		}
	}
	return sum
}

func (m *naive) avail() int64 { return m.reserve - m.openQty() }

func (m *naive) urgentQty(loc string) int64 {
	var sum int64
	for _, tk := range m.open(loc) {
		if tk.urgent {
			sum += tk.qty
		}
	}
	return sum
}

func (m *naive) create(loc string, qty int64, urgent bool) int64 {
	m.seq++
	m.tasks[m.seq] = &mTask{id: m.seq, loc: loc, qty: qty, urgent: urgent}
	return m.seq
}

func (m *naive) checkNormal(loc string) {
	s := m.slots[loc]
	eff := s.onHand + m.transit(loc)
	if eff > s.min {
		return
	}
	want := (s.max - eff) / s.c * s.c
	if want <= 0 {
		return
	}
	qty := want
	if a := m.avail() / s.c * s.c; a < qty {
		qty = a
	}
	if qty > 0 {
		m.create(loc, qty, false)
		return
	}
	s.starved++
}

type res struct {
	err     string
	up, new []int64
}

func rerr(err error) res {
	if err == nil {
		return res{}
	}
	return res{err: err.Error()}
}

func (m *naive) addSlot(loc string, min, max, cap, c, on int64) res {
	if loc == "" || min < 1 || !(min < max) || max > cap || cap > 1e9 ||
		c < 1 || c > 1e6 || on < 0 || on > cap {
		return rerr(ErrInvalid)
	}
	if _, ok := m.slots[loc]; ok {
		return rerr(ErrConflict)
	}
	m.slots[loc] = &mSlot{min: min, max: max, cap: cap, c: c, onHand: on}
	m.checkNormal(loc)
	return res{}
}

func (m *naive) reserveN(qty int64) res {
	if qty < 1 || qty > 1e9 {
		return rerr(ErrInvalid)
	}
	if len(m.slots) == 0 {
		return rerr(ErrNotFound)
	}
	if m.reserve+qty > 1e12 {
		return rerr(ErrOver)
	}
	m.reserve += qty
	return res{}
}

func (m *naive) pick(loc string, qty int64) res {
	if qty < 1 {
		return rerr(ErrInvalid)
	}
	s, ok := m.slots[loc]
	if !ok {
		return rerr(ErrNotFound)
	}
	if qty > s.onHand {
		return rerr(ErrShort)
	}
	s.onHand -= qty
	m.checkNormal(loc)
	return res{}
}

func (m *naive) demand(loc string, need int64) res {
	out := res{up: []int64{}, new: []int64{}}
	if need < 1 {
		return rerr(ErrInvalid)
	}
	s, ok := m.slots[loc]
	if !ok {
		return rerr(ErrNotFound)
	}
	if need > s.cap {
		return rerr(ErrInvalid)
	}
	if s.onHand >= need {
		return out
	}
	uq := m.urgentQty(loc)
	for _, tk := range m.open(loc) {
		if s.onHand+uq >= need {
			break
		}
		if !tk.urgent {
			tk.urgent = true
			uq += tk.qty
			out.up = append(out.up, tk.id)
		}
	}
	eff := s.onHand + m.transit(loc)
	if eff < need {
		want := (need - eff + s.c - 1) / s.c * s.c
		qty := want
		if a := (s.cap - eff) / s.c * s.c; a < qty {
			qty = a
		}
		if a := m.avail() / s.c * s.c; a < qty {
			qty = a
		}
		if qty > 0 {
			out.new = append(out.new, m.create(loc, qty, true))
		}
	}
	return out
}

func (m *naive) finish(id, actual int64, cancel bool) res {
	if id < 1 || (!cancel && actual < 0) {
		return rerr(ErrInvalid)
	}
	tk, ok := m.tasks[id]
	if !ok {
		return rerr(ErrNotFound)
	}
	if tk.status != 0 {
		return rerr(ErrState)
	}
	if !cancel && actual > tk.qty {
		return rerr(ErrOver)
	}
	if cancel {
		tk.status = 2
	} else {
		tk.status = 1
		m.reserve -= tk.qty
		m.slots[tk.loc].onHand += actual
	}
	m.checkNormal(tk.loc)
	return res{}
}

func (m *naive) openOrder() []int64 {
	var urg, nor []int64
	for _, tk := range m.tasks {
		if tk.status != 0 {
			continue
		}
		if tk.urgent {
			urg = append(urg, tk.id)
		} else {
			nor = append(nor, tk.id)
		}
	}
	slices.Sort(urg)
	slices.Sort(nor)
	return append(urg, nor...)
}
