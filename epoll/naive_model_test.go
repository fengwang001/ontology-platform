package epoll

import "sort"

type naiveWatch struct {
	ep       int
	fd       int
	interest int
	flags    int
	eff      int
	disabled bool
}

type naiveModel struct {
	w       int
	e       int
	u       int
	states  map[int]int
	watches map[[2]int]*naiveWatch
	queues  map[int][][2]int
}

func newNaiveModel(w, e, u int) *naiveModel {
	m := &naiveModel{
		w:       w,
		e:       e,
		u:       u,
		states:  make(map[int]int),
		watches: make(map[[2]int]*naiveWatch),
		queues:  make(map[int][][2]int),
	}
	for ep := 0; ep < e; ep++ {
		m.queues[ep] = nil
	}
	return m
}

func (m *naiveModel) validEP(ep int) bool {
	return ep >= 0 && ep < m.e
}

func (m *naiveModel) validInterestFlags(interest, flags int) bool {
	return interest >= 0 && interest <= 15 &&
		flags&^7 == 0 &&
		(flags&(EXCL|ONESHOT) != EXCL|ONESHOT) &&
		(flags&EXCL == 0 || interest&(ERR|HUP) == 0)
}

func (m *naiveModel) contains(ep, fd int) bool {
	for _, item := range m.queues[ep] {
		if item[0] == ep && item[1] == fd {
			return true
		}
	}
	return false
}

func (m *naiveModel) append(ep, fd int) {
	if !m.contains(ep, fd) {
		m.queues[ep] = append(m.queues[ep], [2]int{ep, fd})
	}
}

func (m *naiveModel) remove(ep, fd int) {
	queue := m.queues[ep]
	for i, item := range queue {
		if item[0] == ep && item[1] == fd {
			m.queues[ep] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

func (m *naiveModel) count(ep int) int {
	count := 0
	for key := range m.watches {
		if key[0] == ep {
			count++
		}
	}
	return count
}

func (m *naiveModel) add(ep, fd, interest, flags int) error {
	if !m.validEP(ep) || fd < 0 || !m.validInterestFlags(interest, flags) {
		return ErrInvalid
	}
	key := [2]int{ep, fd}
	if _, ok := m.watches[key]; ok {
		return ErrExists
	}
	if m.count(ep) >= m.w {
		return ErrNoSpace
	}
	if len(m.watches) >= m.u {
		return ErrTooMany
	}

	w := &naiveWatch{
		ep:       ep,
		fd:       fd,
		interest: interest,
		flags:    flags,
		eff:      interest | ERR | HUP,
	}
	m.watches[key] = w
	if m.states[fd]&w.eff != 0 {
		m.append(ep, fd)
	}
	return nil
}

func (m *naiveModel) mod(ep, fd, interest, flags int) error {
	if !m.validEP(ep) || fd < 0 || !m.validInterestFlags(interest, flags) {
		return ErrInvalid
	}
	w, ok := m.watches[[2]int{ep, fd}]
	if !ok {
		return ErrNoEnt
	}
	if w.flags&EXCL != flags&EXCL {
		return ErrExclChange
	}

	w.interest = interest
	w.flags = flags
	w.eff = interest | ERR | HUP
	w.disabled = false
	if m.states[fd]&w.eff != 0 {
		m.append(ep, fd)
	}
	return nil
}

func (m *naiveModel) del(ep, fd int) error {
	if !m.validEP(ep) || fd < 0 {
		return ErrInvalid
	}
	key := [2]int{ep, fd}
	if _, ok := m.watches[key]; !ok {
		return ErrNoEnt
	}
	delete(m.watches, key)
	m.remove(ep, fd)
	return nil
}

func (m *naiveModel) close(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}

	removed := 0
	for ep := 0; ep < m.e; ep++ {
		key := [2]int{ep, fd}
		if _, ok := m.watches[key]; ok {
			delete(m.watches, key)
			m.remove(ep, fd)
			removed++
		}
	}
	m.states[fd] = 0
	return removed, nil
}

func (m *naiveModel) setState(fd, state int) error {
	if fd < 0 || state < 0 || state > 15 {
		return ErrInvalid
	}

	old := m.states[fd]
	m.states[fd] = state
	exclusiveEP := -1

	for ep := 0; ep < m.e; ep++ {
		w, ok := m.watches[[2]int{ep, fd}]
		if !ok || w.disabled || m.contains(ep, fd) {
			continue
		}

		ready := false
		if w.flags&ET != 0 {
			ready = state&^old&w.eff != 0
		} else {
			ready = state&w.eff != 0
		}
		if !ready {
			continue
		}

		if w.flags&EXCL != 0 {
			if exclusiveEP == -1 || ep < exclusiveEP {
				exclusiveEP = ep
			}
			continue
		}
		m.append(ep, fd)
	}
	if exclusiveEP != -1 {
		m.append(exclusiveEP, fd)
	}
	return nil
}

func (m *naiveModel) wait(ep, max int) ([]Event, error) {
	if !m.validEP(ep) || max < 1 || max > 1_000_000 {
		return nil, ErrInvalid
	}

	events := make([]Event, 0)
	refill := make([][2]int, 0)
	for len(events) < max && len(m.queues[ep]) > 0 {
		key := m.queues[ep][0]
		m.queues[ep] = m.queues[ep][1:]

		fd := key[1]
		w := m.watches[key]
		revents := m.states[fd] & w.eff
		if revents == 0 {
			continue
		}

		events = append(events, Event{FD: fd, Revents: revents})
		if w.flags&ONESHOT != 0 {
			w.disabled = true
		} else if w.flags&ET == 0 {
			refill = append(refill, key)
		}
	}

	for _, key := range refill {
		m.append(key[0], key[1])
	}
	return events, nil
}

func (m *naiveModel) snapshotQueues() [][]int {
	result := make([][]int, m.e)
	for ep := 0; ep < m.e; ep++ {
		result[ep] = make([]int, len(m.queues[ep]))
		for i, item := range m.queues[ep] {
			result[ep][i] = item[1]
		}
		if len(result[ep]) == 0 {
			result[ep] = nil
		}
	}
	return result
}

func (m *naiveModel) snapshotWatches() [][]Watch {
	result := make([][]Watch, m.e)
	for ep := 0; ep < m.e; ep++ {
		fds := make([]int, 0)
		for key, w := range m.watches {
			if key[0] == ep {
				fds = append(fds, w.fd)
			}
		}
		sort.Ints(fds)

		for _, fd := range fds {
			w := m.watches[[2]int{ep, fd}]
			result[ep] = append(result[ep], Watch{
				FD:       fd,
				Interest: w.interest,
				Flags:    w.flags,
				Eff:      w.eff,
				Queued:   m.contains(ep, fd),
				Disabled: w.disabled,
			})
		}
		if len(result[ep]) == 0 {
			result[ep] = nil
		}
	}
	return result
}
