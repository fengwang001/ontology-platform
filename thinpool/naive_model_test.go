package thinpool

// naiveModel 是独立、直白的逐块参考实现：
// 每个卷用 map[int]bool 逐块记录映射；每次可行性判定都遍历所有卷重算欠额。
// 它刻意不共享任何生产代码的数据结构，仅用于差分测试。
type naiveModel struct {
	physical     int
	overPct      int
	warnPct      int
	critPct      int
	volumes      map[string]*naiveVolume
	order        []string
	allocated    int
	events       []Event
	nextEvent    int
	level        Level
	totalVirtual int
	totalReserve int
}

type naiveVolume struct {
	virtual     int
	reservation int
	mapped      map[int]bool
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		physical: cfg.PhysicalBlocks,
		overPct:  cfg.OvercommitPct,
		warnPct:  cfg.WarningPct,
		critPct:  cfg.CriticalPct,
		volumes:  map[string]*naiveVolume{},
	}
}

func (m *naiveModel) deficit(v *naiveVolume) int {
	d := v.reservation - len(v.mapped)
	if d < 0 {
		return 0
	}
	return d
}

func (m *naiveModel) totalDeficit() int {
	sum := 0
	for _, name := range m.order {
		sum += m.deficit(m.volumes[name])
	}
	return sum
}

func (m *naiveModel) free() int { return m.physical - m.allocated }

func (m *naiveModel) noteLevel() {
	lvl := classify(m.allocated, m.physical, m.warnPct, m.critPct)
	if lvl != m.level {
		m.nextEvent++
		m.events = append(m.events, Event{
			Seq: m.nextEvent, From: m.level, To: lvl, Allocated: m.allocated,
		})
		m.level = lvl
	}
}

func (m *naiveModel) create(name string, virtual, reservation int) error {
	if name == "" || virtual < 0 || reservation < 0 {
		return &Error{Kind: KindInvalidArgument}
	}
	if _, ok := m.volumes[name]; ok {
		return &Error{Kind: KindVolumeExists}
	}
	if (m.totalVirtual+virtual)*100 > m.physical*m.overPct {
		return &Error{Kind: KindOvercommit}
	}
	if m.totalReserve+reservation > m.physical {
		return &Error{Kind: KindReservationPool}
	}
	if reservation > virtual {
		return &Error{Kind: KindReservationVolume}
	}
	if m.totalDeficit()+reservation > m.free() {
		return &Error{Kind: KindReservationShortfall}
	}
	m.volumes[name] = &naiveVolume{virtual: virtual, reservation: reservation, mapped: map[int]bool{}}
	m.order = append(m.order, name)
	m.allocated += 0
	m.totalVirtual += virtual
	m.totalReserve += reservation
	return nil
}

func (m *naiveModel) write(name string, block int) (bool, error) {
	if name == "" || block < 0 {
		return false, &Error{Kind: KindInvalidArgument}
	}
	v, ok := m.volumes[name]
	if !ok {
		return false, &Error{Kind: KindVolumeNotFound}
	}
	if block >= v.virtual {
		return false, &Error{Kind: KindInvalidArgument}
	}
	if v.mapped[block] {
		return false, nil
	}
	others := m.totalDeficit() - m.deficit(v) // 逐卷遍历重算
	if m.free()-1 < others {
		return false, &Error{Kind: KindPoolExhausted}
	}
	v.mapped[block] = true
	m.allocated++
	m.noteLevel()
	return true, nil
}

func (m *naiveModel) reclaim(name string, start, length int) (int, error) {
	if name == "" || start < 0 || length < 0 {
		return 0, &Error{Kind: KindInvalidArgument}
	}
	v, ok := m.volumes[name]
	if !ok {
		return 0, &Error{Kind: KindVolumeNotFound}
	}
	if start > v.virtual || start+length > v.virtual {
		return 0, &Error{Kind: KindInvalidArgument}
	}
	if length == 0 {
		return 0, nil
	}
	freed := 0
	for b := start; b < start+length; b++ {
		if v.mapped[b] {
			delete(v.mapped, b)
			freed++
		}
	}
	m.allocated -= freed
	if freed > 0 {
		m.noteLevel()
	}
	return freed, nil
}

func (m *naiveModel) del(name string) error {
	if name == "" {
		return &Error{Kind: KindInvalidArgument}
	}
	v, ok := m.volumes[name]
	if !ok {
		return &Error{Kind: KindVolumeNotFound}
	}
	m.allocated -= len(v.mapped)
	m.totalVirtual -= v.virtual
	m.totalReserve -= v.reservation
	delete(m.volumes, name)
	for i, n := range m.order {
		if n == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.noteLevel()
	return nil
}

func (m *naiveModel) resize(name string, newVirtual int) error {
	if name == "" || newVirtual < 0 {
		return &Error{Kind: KindInvalidArgument}
	}
	v, ok := m.volumes[name]
	if !ok {
		return &Error{Kind: KindVolumeNotFound}
	}
	if v.reservation > newVirtual {
		return &Error{Kind: KindReservationVolume}
	}
	if newVirtual > v.virtual {
		if (m.totalVirtual+newVirtual-v.virtual)*100 > m.physical*m.overPct {
			return &Error{Kind: KindOvercommit}
		}
	} else if newVirtual < v.virtual {
		for b := newVirtual; b < v.virtual; b++ {
			if v.mapped[b] {
				return &Error{Kind: KindDataInRange}
			}
		}
	}
	m.totalVirtual += newVirtual - v.virtual
	v.virtual = newVirtual
	return nil
}

func (m *naiveModel) setReservation(name string, newRes int) error {
	if name == "" || newRes < 0 {
		return &Error{Kind: KindInvalidArgument}
	}
	v, ok := m.volumes[name]
	if !ok {
		return &Error{Kind: KindVolumeNotFound}
	}
	if newRes > v.virtual {
		return &Error{Kind: KindReservationVolume}
	}
	if m.totalReserve-v.reservation+newRes > m.physical {
		return &Error{Kind: KindReservationPool}
	}
	newDef := newRes - len(v.mapped)
	if newDef < 0 {
		newDef = 0
	}
	if m.totalDeficit()-m.deficit(v)+newDef > m.free() {
		return &Error{Kind: KindReservationShortfall}
	}
	m.totalReserve += newRes - v.reservation
	v.reservation = newRes
	return nil
}
