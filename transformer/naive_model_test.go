package transformer

// 独立朴素模型：不依赖 System 的任何内部结构，按规则逐时刻、逐预约
// 暴力重算占用，用于随机操作序列的差分对照。

type mRes struct {
	id      int
	feeder  string
	start   int
	end     int
	power   int
	created int
	expiry  int
	status  Status
}

type naiveModel struct {
	now     int
	holdDur int
	limits  map[string]int
	recs    map[int]int // 生效时刻 -> 容量
	nextID  int
	res     []*mRes // 按创建顺序，即 ID 升序
}

func newNaiveModel(holdDur int, limits map[string]int, initCap int) *naiveModel {
	ls := make(map[string]int, len(limits))
	for f, l := range limits {
		ls[f] = l
	}
	return &naiveModel{
		holdDur: holdDur,
		limits:  ls,
		recs:    map[int]int{0: initCap},
		nextID:  1,
	}
}

func (m *naiveModel) capAt(t int) int {
	best, c := -1, 0
	for rt, rc := range m.recs {
		if rt <= t && rt > best {
			best, c = rt, rc
		}
	}
	return c
}

func (m *naiveModel) find(id int) *mRes {
	for _, r := range m.res {
		if r.id == id {
			return r
		}
	}
	return nil
}

// sums 返回 t 时刻馈线占用与变压器总占用（占位中+已确认），excludeID 用于改约排除自身。
func (m *naiveModel) sums(t int, feeder string, excludeID int) (int, int) {
	fSum, tSum := 0, 0
	for _, r := range m.res {
		if r.id == excludeID {
			continue
		}
		if r.status != StatusHolding && r.status != StatusConfirmed {
			continue
		}
		if r.start <= t && t < r.end {
			tSum += r.power
			if r.feeder == feeder {
				fSum += r.power
			}
		}
	}
	return fSum, tSum
}

func (m *naiveModel) confirmedSum(t int) int {
	sum := 0
	for _, r := range m.res {
		if r.status == StatusConfirmed && r.start <= t && t < r.end {
			sum += r.power
		}
	}
	return sum
}

func (m *naiveModel) create(feeder string, start, end, power int) (int, *Error) {
	lim, ok := m.limits[feeder]
	if !ok {
		return 0, &Error{Category: ErrInvalidParam, Detail: "馈线不存在"}
	}
	if end <= start || start < m.now {
		return 0, &Error{Category: ErrInvalidParam, Detail: "区间非法"}
	}
	if power <= 0 {
		return 0, &Error{Category: ErrInvalidParam, Detail: "功率非正"}
	}
	if power > lim {
		return 0, &Error{Category: ErrInvalidParam, Detail: "功率超过馈线上限"}
	}
	for t := start; t < end; t++ {
		fSum, tSum := m.sums(t, feeder, -1)
		if fSum+power > lim {
			return 0, &Error{Category: ErrCapacity, Level: LevelFeeder, Time: t}
		}
		if tSum+power > m.capAt(t) {
			return 0, &Error{Category: ErrCapacity, Level: LevelTransformer, Time: t}
		}
	}
	id := m.nextID
	m.nextID++
	m.res = append(m.res, &mRes{
		id: id, feeder: feeder, start: start, end: end, power: power,
		created: m.now, expiry: m.now + m.holdDur, status: StatusHolding,
	})
	return id, nil
}

func (m *naiveModel) confirm(id int) *Error {
	r := m.find(id)
	if r == nil {
		return &Error{Category: ErrNotFound}
	}
	switch r.status {
	case StatusHolding:
		if m.now >= r.expiry {
			r.status = StatusExpired
			return &Error{Category: ErrHoldExpired}
		}
		r.status = StatusConfirmed
		if r.end <= m.now {
			r.status = StatusCompleted
		}
		return nil
	case StatusExpired:
		return &Error{Category: ErrHoldExpired}
	case StatusCancelled:
		return &Error{Category: ErrCancelled}
	default:
		return &Error{Category: ErrInvalidState}
	}
}

func (m *naiveModel) release(id int) *Error {
	r := m.find(id)
	if r == nil {
		return &Error{Category: ErrNotFound}
	}
	if r.status != StatusHolding && r.status != StatusConfirmed {
		return &Error{Category: ErrInvalidState}
	}
	r.status = StatusReleased
	return nil
}

func (m *naiveModel) modify(id, start, end, power int) *Error {
	if end <= start || power <= 0 {
		return &Error{Category: ErrInvalidParam}
	}
	r := m.find(id)
	if r == nil {
		return &Error{Category: ErrNotFound}
	}
	if r.status != StatusHolding && r.status != StatusConfirmed {
		return &Error{Category: ErrInvalidState}
	}
	lim := m.limits[r.feeder]
	if power > lim {
		return &Error{Category: ErrInvalidParam}
	}
	if r.status == StatusConfirmed && r.start <= m.now {
		if start != r.start || power != r.power || end > r.end || end < m.now {
			return &Error{Category: ErrInvalidParam}
		}
	} else if start < m.now {
		return &Error{Category: ErrInvalidParam}
	}
	for t := start; t < end; t++ {
		fSum, tSum := m.sums(t, r.feeder, id)
		if fSum+power > lim {
			return &Error{Category: ErrCapacity, Level: LevelFeeder, Time: t}
		}
		if tSum+power > m.capAt(t) {
			return &Error{Category: ErrCapacity, Level: LevelTransformer, Time: t}
		}
	}
	r.start, r.end, r.power = start, end, power
	if r.status == StatusConfirmed && r.end <= m.now {
		r.status = StatusCompleted
	}
	return nil
}

func (m *naiveModel) advance(to int) *Error {
	if to < m.now {
		return &Error{Category: ErrClockRewind}
	}
	m.now = to
	for _, r := range m.res {
		if r.status == StatusHolding && r.expiry <= to {
			r.status = StatusExpired
		}
		if r.status == StatusConfirmed && r.end <= to {
			r.status = StatusCompleted
		}
	}
	return nil
}

func (m *naiveModel) addCapacity(effectiveAt, capacity int) *Error {
	if effectiveAt < m.now || capacity < 0 {
		return &Error{Category: ErrInvalidParam}
	}
	hi := 0
	for _, r := range m.res {
		if (r.status == StatusHolding || r.status == StatusConfirmed) && r.end > hi {
			hi = r.end
		}
	}
	for rt := range m.recs {
		if rt > effectiveAt && rt < hi {
			hi = rt
		}
	}
	for t := effectiveAt; t < hi; t++ {
		if m.confirmedSum(t) > capacity {
			return &Error{Category: ErrCapacity, Level: LevelTransformer, Time: t}
		}
	}
	m.recs[effectiveAt] = capacity
	for t := effectiveAt; t < hi; t++ {
		for {
			_, tSum := m.sums(t, "", -1)
			if tSum <= m.capAt(t) {
				break
			}
			var best *mRes
			for _, r := range m.res {
				if r.status != StatusHolding || r.start > t || t >= r.end {
					continue
				}
				if best == nil || r.created > best.created ||
					(r.created == best.created && r.id > best.id) {
					best = r
				}
			}
			if best == nil {
				break
			}
			best.status = StatusCancelled
		}
	}
	return nil
}
