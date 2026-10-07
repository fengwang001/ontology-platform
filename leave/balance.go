package leave

// asof 返回年度台账在不晚于 t 的最后一条历史快照；没有则返回零台账。
func asof(a *yearAcct, t int) ledger {
	if a == nil {
		return ledger{}
	}
	lo, hi := 0, len(a.hist) // 找最后一个 now <= t
	for lo < hi {
		mid := (lo + hi) / 2
		if a.hist[mid].now <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return ledger{}
	}
	return a.hist[lo-1].val
}

// carriedOutAsOf 返回年度 y 在时刻 t 视角下的已结转出去量。
// 已钉死取钉死值；若 t 所在年度已越过 y（年度切换必然已触达），
// 按 t 时刻台账推导（与假想的 t 时刻钉死结果一致）；否则为 0。
func (s *Service) carriedOutAsOf(e *employee, y, t int) ledger {
	l := asof(e.acctRO(y), t)
	if l.pinned || yearOf(t) <= y {
		return l
	}
	unused := s.quota(e, y) - l.usedCur - l.pendCur
	if unused < 0 {
		unused = 0
	}
	if unused > s.cfg.CarryCap {
		unused = s.cfg.CarryCap
	}
	l.carriedOut = unused
	l.pinned = true
	return l
}

// Balance 查询员工在时刻 now 所在年度的余额。
// now 可以是历史时刻：结果与当时按相同操作序列得到的一致。
// 查询是只读的，不改变任何状态，也不推进时钟。
func (s *Service) Balance(now int, empID string) (Balance, error) {
	if now < 0 {
		return Balance{}, errf(CatInvalidParam, "now=%d is negative", now)
	}
	if empID == "" {
		return Balance{}, errf(CatInvalidParam, "empty employee id")
	}
	e := s.getEmployee(empID)
	if e == nil {
		return Balance{}, errf(CatEmployeeNotFound, "employee %q not found", empID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.regNow {
		return Balance{}, errf(CatEmployeeNotFound, "employee %q not registered at now=%d", empID, now)
	}
	y := yearOf(now)
	cur := asof(e.acctRO(y), now)
	prev := s.carriedOutAsOf(e, y-1, now)
	this := s.carriedOutAsOf(e, y, now)
	granted := s.quota(e, y)
	return Balance{
		Year:             y,
		CurrentGranted:   granted,
		CurrentPending:   cur.pendCur,
		CurrentUsed:      cur.usedCur,
		CurrentAvailable: granted - cur.usedCur - cur.pendCur - this.carriedOut,
		CarriedGranted:   prev.carriedOut,
		CarriedPending:   cur.pendCar,
		CarriedUsed:      cur.usedCar,
		CarriedAvailable: prev.carriedOut - cur.usedCar - cur.pendCar - cur.voidedCar,
	}, nil
}

// YearDump 是单个年度台账的诊断视图，用于验证发放总量守恒：
//
//	当年额度：Granted == Used + Pending + CarriedOut + Available
//	结转额度：CarriedIn == UsedCar + PendingCar + VoidedCar + AvailableCar
type YearDump struct {
	Year       int
	Granted    int
	CarriedIn  int
	UsedCur    int
	PendCur    int
	CarriedOut int
	Pinned     bool
	UsedCar    int
	PendCar    int
	VoidedCar  int
}

// DumpYears 返回员工当前所有有记录年度的台账诊断视图（按年度升序）。
// 仅供测试与审计使用。
func (s *Service) DumpYears(empID string) ([]YearDump, error) {
	e := s.getEmployee(empID)
	if e == nil {
		return nil, errf(CatEmployeeNotFound, "employee %q not found", empID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ymax := yearOf(e.hireDay)
	for y := range e.years {
		if y > ymax {
			ymax = y
		}
	}
	out := make([]YearDump, 0, len(e.years))
	for y := yearOf(e.hireDay); y <= ymax; y++ {
		a := e.acctRO(y)
		if a == nil {
			continue
		}
		d := YearDump{
			Year:       y,
			Granted:    s.quota(e, y),
			CarriedIn:  s.carryOf(e, y),
			UsedCur:    a.cur.usedCur,
			PendCur:    a.cur.pendCur,
			CarriedOut: a.cur.carriedOut,
			Pinned:     a.cur.pinned,
			UsedCar:    a.cur.usedCar,
			PendCar:    a.cur.pendCar,
			VoidedCar:  a.cur.voidedCar,
		}
		out = append(out, d)
	}
	return out, nil
}
