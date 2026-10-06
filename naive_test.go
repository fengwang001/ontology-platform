package railway

// naiveTrain 是按题意独立写成的朴素对照模型：
// 每次操作都从全部有效车票清单线性重算座位占用与票额，不做任何增量结构。

type naiveTicket struct {
	id         int64
	from, to   int
	passenger  string
	standing   bool
	car, no    int
	quotaShare bool
	refunded   bool
}

type naiveModel struct {
	cfg        Config
	departures []int
	alloc      [][]int // 原始分配票额（不变）
	seats      []Seat
	shared0    int
	tickets    []*naiveTicket
	clock      int
	nextID     int64
}

func newNaive(cfg Config, spec TrainSpec) *naiveModel {
	var seats []Seat
	for c := 1; c <= spec.Cars; c++ {
		for n := 1; n <= spec.SeatsPerCar; n++ {
			seats = append(seats, Seat{Car: c, No: n})
		}
	}
	alloc := make([][]int, len(spec.Alloc))
	for i := range spec.Alloc {
		alloc[i] = append([]int(nil), spec.Alloc[i]...)
	}
	return &naiveModel{cfg: cfg, departures: spec.Departures, alloc: alloc,
		seats: seats, shared0: spec.Shared, clock: -1, nextID: 1}
}

func (m *naiveModel) mergedUpTo(now int) int {
	i := 0
	for i < len(m.departures) && m.departures[i]-m.cfg.AdvanceSeconds <= now {
		i++
	}
	return i
}

// allocRem 重算某发站-到站当前剩余分配票额（已售出但未退的占用）。
func (m *naiveModel) allocRem(merged int, from, to int) int {
	rem := m.alloc[from][to]
	if from < merged {
		return 0
	}
	for _, t := range m.tickets {
		if !t.refunded && !t.quotaShare && t.from == from && t.to == to {
			rem--
		}
	}
	return rem
}

// sharedRem 重算共用票额：初始 + 已并入剩余 - 所有共用来源有效票。
func (m *naiveModel) sharedRem(merged int) int {
	shared := m.shared0
	for f := 0; f < merged; f++ {
		for to := f + 1; to < len(m.departures); to++ {
			shared += m.alloc[f][to]
		}
	}
	for _, t := range m.tickets {
		if t.refunded {
			continue
		}
		// 发站并入前售出、扣自原始分配的票：其原始分配已计入并入额，需扣除；
		// 其余有效票均扣自共用，一律减一。
		if t.quotaShare || t.from < merged {
			shared--
		}
	}
	return shared
}

type naiveOutcome struct {
	ok        bool
	reason    Reason
	standing  bool
	car, no   int
	useShared bool
	id        int64
}

func (m *naiveModel) buy(now int, d TicketDesc) naiveOutcome {
	n := len(m.departures)
	if now < 0 || d.Passenger == "" || d.From < 0 || d.To <= d.From || d.To >= n {
		return naiveOutcome{reason: ReasonInvalidParam}
	}
	if now < m.clock {
		return naiveOutcome{reason: ReasonClockRollback}
	}
	if now >= m.departures[d.From] {
		return naiveOutcome{reason: ReasonDeparted}
	}
	for _, t := range m.tickets {
		if !t.refunded && t.passenger == d.Passenger && d.From < t.to && t.from < d.To {
			return naiveOutcome{reason: ReasonPassengerOverlap}
		}
	}
	merged := m.mergedUpTo(now)
	ar := m.allocRem(merged, d.From, d.To)
	sr := m.sharedRem(merged)
	useShared := false
	if ar <= 0 {
		if sr <= 0 {
			return naiveOutcome{reason: ReasonQuotaExhausted}
		}
		useShared = true
	}
	// 线性枚举每个座位，重建其边占用，按 (优先级,车厢,座位) 取最小。
	bestCar, bestNo, bestClass := 0, 0, -1
	found := false
	for _, seat := range m.seats {
		edges := make([]bool, n-1)
		for _, t := range m.tickets {
			if t.refunded || t.standing || t.car != seat.Car || t.no != seat.No {
				continue
			}
			for e := t.from; e < t.to; e++ {
				edges[e] = true
			}
		}
		free := true
		for e := d.From; e < d.To; e++ {
			if edges[e] {
				free = false
			}
		}
		if !free {
			continue
		}
		left := d.From > 0 && edges[d.From-1]
		right := d.To < n-1 && edges[d.To]
		class := 0
		if left && right {
			class = 2
		} else if left || right {
			class = 1
		}
		if class > bestClass {
			bestClass, bestCar, bestNo, found = class, seat.Car, seat.No, true
		}
	}
	tk := &naiveTicket{id: m.nextID, from: d.From, to: d.To, passenger: d.Passenger,
		quotaShare: useShared}
	if found {
		tk.car, tk.no = bestCar, bestNo
	} else {
		cap := int(float64(len(m.seats)) * m.cfg.StandingRatio) // 截断即 floor（比例非负）
		for e := d.From; e < d.To; e++ {
			used := 0
			for _, t := range m.tickets {
				if !t.refunded && t.standing && e >= t.from && e < t.to {
					used++
				}
			}
			if used >= cap {
				return naiveOutcome{reason: ReasonNoSeat}
			}
		}
		tk.standing = true
	}
	m.tickets = append(m.tickets, tk)
	m.clock = now
	m.nextID++
	return naiveOutcome{ok: true, standing: tk.standing, car: tk.car, no: tk.no,
		useShared: useShared, id: tk.id}
}

func (m *naiveModel) find(id int64) *naiveTicket {
	for _, t := range m.tickets {
		if t.id == id {
			return t
		}
	}
	return nil
}

func (m *naiveModel) refund(now int, id int64) Reason {
	if now < 0 || id <= 0 {
		return ReasonInvalidParam
	}
	if now < m.clock {
		return ReasonClockRollback
	}
	t := m.find(id)
	if t == nil {
		return ReasonTicketNotFound
	}
	if t.refunded {
		return ReasonTicketRefunded
	}
	if now >= m.departures[t.from] {
		return ReasonDeparted
	}
	t.refunded = true
	m.clock = now
	return ReasonOK
}
