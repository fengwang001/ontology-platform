package snapshotfs

import "errors"

type mBlock struct {
	id, size, b, d int64
	discarded      bool
}

type mSnap struct {
	name string
	t    int64
	hold int
}

// model 是严格按题目定义逐块重算的朴素参考模型。
type model struct {
	cap   int64
	cur   int64
	maxID int64
	blks  map[int64]*mBlock
	snaps map[string]*mSnap
}

func newModel(cap int64) *model {
	return &model{cap: cap, cur: 1, blks: map[int64]*mBlock{}, snaps: map[string]*mSnap{}}
}

func (m *model) aliveAt(bl *mBlock, t int64) bool {
	return !bl.discarded && bl.b <= t && t < bl.d
}

func (m *model) used() int64 {
	var total int64
	for _, bl := range m.blks {
		if bl.discarded {
			continue
		}
		if bl.d == infinity {
			total += bl.size
			continue
		}
		for _, s := range m.snaps {
			if m.aliveAt(bl, s.t) {
				total += bl.size
				break
			}
		}
	}
	return total
}

func (m *model) aliveBytes() int64 {
	var total int64
	for _, bl := range m.blks {
		if !bl.discarded && bl.d == infinity {
			total += bl.size
		}
	}
	return total
}

func (m *model) referenced(name string) int64 {
	s := m.snaps[name]
	var total int64
	for _, bl := range m.blks {
		if m.aliveAt(bl, s.t) {
			total += bl.size
		}
	}
	return total
}

func (m *model) unique(name string) int64 {
	target := m.snaps[name]
	var total int64
	for _, bl := range m.blks {
		if bl.discarded || bl.d == infinity || !m.aliveAt(bl, target.t) {
			continue
		}
		sole := true
		for nm, s := range m.snaps {
			if nm == name {
				continue
			}
			if m.aliveAt(bl, s.t) {
				sole = false
				break
			}
		}
		if sole {
			total += bl.size
		}
	}
	return total
}

func (m *model) alloc(size int64) (int64, error) {
	if size < 1 || size > MaxAlloc {
		return 0, ErrInvalidArgument
	}
	if m.used()+size > m.cap {
		return 0, ErrOutOfSpace
	}
	m.maxID++
	m.blks[m.maxID] = &mBlock{id: m.maxID, size: size, b: m.cur, d: infinity}
	return m.maxID, nil
}

func (m *model) free(id int64) error {
	if id < 1 {
		return ErrInvalidArgument
	}
	if id > m.maxID {
		return ErrNotFound
	}
	bl := m.blks[id]
	if bl.discarded {
		return ErrDiscarded
	}
	if bl.d != infinity {
		return ErrDead
	}
	bl.d = m.cur
	return nil
}

func (m *model) snapshot(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	if _, ok := m.snaps[name]; ok {
		return ErrSnapshotExists
	}
	m.snaps[name] = &mSnap{name: name, t: m.cur}
	m.cur++
	return nil
}

func (m *model) destroy(name string) error {
	s, ok := m.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	if s.hold > 0 {
		return ErrHeld
	}
	delete(m.snaps, name)
	return nil
}

func (m *model) hold(name string) error {
	s, ok := m.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	s.hold++
	return nil
}

func (m *model) release(name string) error {
	s, ok := m.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	if s.hold == 0 {
		return ErrNotHeld
	}
	s.hold--
	return nil
}

func (m *model) rollback(name string) error {
	target, ok := m.snaps[name]
	if !ok {
		return ErrSnapshotMissing
	}
	for _, s := range m.snaps {
		if s.t > target.t && s.hold > 0 {
			return ErrHeld
		}
	}
	t := target.t
	for nm, s := range m.snaps {
		if s.t > t {
			delete(m.snaps, nm)
		}
	}
	for _, bl := range m.blks {
		if bl.discarded {
			continue
		}
		if bl.b > t {
			bl.discarded = true
		} else if bl.d > t {
			bl.d = infinity
		}
	}
	return nil
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}
