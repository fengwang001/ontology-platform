package ontology

import "sort"

// naiveModel 是独立编写的参考实现：不使用任何堆/索引，
// 每次被接受操作都从上次时钟逐日推进、线性扫描全部租约。
// 它与 service.go 的数据结构、推进方式完全不同，用于随机序列对照。
type naiveModel struct {
	cfg    Config
	day    int
	leases map[string]*lease
	offers map[string]string
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, leases: map[string]*lease{}, offers: map[string]string{}}
}

// stepDay 让单份租约经历从 day-1 到 day 的状态转换。
func (m *naiveModel) stepDay(l *lease, day int) {
	if l.offer != nil && l.offer.Stage == StageTenant && day > l.offer.tenantDeadline(m.cfg.C) {
		l.offer.Stage = StageExpired
		l.offer.ResolveDay = day
	}
	if l.offer != nil && l.offer.Stage == StageCounter && day > l.offer.counterDeadline(m.cfg.C) {
		l.offer.Stage = StageExpired
		l.offer.ResolveDay = day
	}
	if l.phase == PhaseActive && !l.protected && !l.offerEverIssued && day == l.end-m.cfg.A+1 {
		l.protected = true
	}
	if l.phase == PhaseActive && day == l.end {
		if l.protected || !l.offerEverIssued || (l.offer != nil && l.offer.Stage == StageExpired) {
			m.enterHoldover(l)
		} else {
			l.phase = PhaseEnded
			l.generation++
		}
	}
	if l.phase == PhaseHoldover && l.noticeDay != 0 && day == l.terminateAt {
		l.phase = PhaseTerminated
		l.generation++
	}
}

func (m *naiveModel) enterHoldover(l *lease) {
	l.phase = PhaseHoldover
	l.holdoverStart = l.end
	l.protected = true
	if l.offer != nil && (l.offer.Stage == StageTenant || l.offer.Stage == StageCounter) {
		l.offer.Stage = StageExpired
		l.offer.ResolveDay = l.end
	}
	l.generation++
}

func (m *naiveModel) advance(now int) error {
	if now < m.day {
		return errf(ErrClockRollback, "clock rollback: now=%d < last accepted now=%d", now, m.day)
	}
	ids := make([]string, 0, len(m.leases))
	for id := range m.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for day := m.day + 1; day <= now; day++ {
		for _, id := range ids {
			m.stepDay(m.leases[id], day)
		}
	}
	m.day = now
	return nil
}

func (m *naiveModel) clone() *naiveModel {
	cp := &naiveModel{cfg: m.cfg, day: m.day, leases: map[string]*lease{}, offers: map[string]string{}}
	for id, l := range m.leases {
		nc := *l
		if l.offer != nil {
			oc := *l.offer
			nc.offer = &oc
		}
		cp.leases[id] = &nc
	}
	for oid, lid := range m.offers {
		cp.offers[oid] = lid
	}
	return cp
}

func (m *naiveModel) try(now int, op func(c *naiveModel) error) (*naiveModel, error) {
	c := m.clone()
	if err := c.advance(now); err != nil {
		return nil, err
	}
	if err := op(c); err != nil {
		return nil, err
	}
	return c, nil
}

// run 在副本上推进并执行 op，成功才提交；等价于“被拒绝操作不留痕”。
func (m *naiveModel) run(now int, op func(c *naiveModel) error) error {
	c, err := m.try(now, op)
	if err != nil {
		return err
	}
	*m = *c
	return nil
}

// 以下为朴素模型独立重写的全部操作校验，与 service.go 不共享任何业务函数。

func (m *naiveModel) addLease(id string, start, end, rent, lastAdjust, now int) error {
	return m.run(now, func(c *naiveModel) error {
		if id == "" || start < 0 || end <= start || rent <= 0 || lastAdjust > now {
			return errf(ErrInvalidArgument, "invalid lease: id=%q start=%d end=%d rent=%d lastAdjust=%d",
				id, start, end, rent, lastAdjust)
		}
		if _, ok := c.leases[id]; ok {
			return errf(ErrInvalidArgument, "lease id duplicated: %q", id)
		}
		c.leases[id] = &lease{
			id: id, start: start, end: end, rent: rent, lastAdjust: lastAdjust,
			phase: PhaseActive, generation: 1,
		}
		return nil
	})
}

func (m *naiveModel) issueOffer(leaseID, offerID string, newRent, newEnd, now int) error {
	if offerID == "" || newRent <= 0 || newEnd <= 0 {
		return errf(ErrInvalidArgument, "invalid offer: id=%q rent=%d end=%d", offerID, newRent, newEnd)
	}
	return m.run(now, func(c *naiveModel) error {
		l, ok := c.leases[leaseID]
		if !ok {
			return errf(ErrLeaseNotFound, "lease not found: %q", leaseID)
		}
		if _, dup := c.offers[offerID]; dup {
			return errf(ErrInvalidArgument, "offer id duplicated: %q", offerID)
		}
		if l.phase != PhaseActive || l.protected {
			return errf(ErrIllegalState, "lease %s not open to offer (phase=%s protected=%v)",
				leaseID, l.phase, l.protected)
		}
		if l.offer != nil && (l.offer.Stage == StageTenant || l.offer.Stage == StageCounter) {
			return errf(ErrIllegalState, "lease %s already has pending offer %s", leaseID, l.offer.ID)
		}
		left, right := l.end-c.cfg.B, l.end-c.cfg.A
		if now < left || now > right {
			return errf(ErrIllegalState, "issue day %d outside renewal window [%d,%d] (end=%d)",
				now, left, right, l.end)
		}
		if newEnd <= l.end {
			return errf(ErrInvalidArgument, "new end %d must extend beyond current end %d", newEnd, l.end)
		}
		if newRent > l.rent {
			capRent := rentCap(l.rent, l.lastAdjust, now, c.cfg.StepBP, c.cfg.CapBP)
			if newRent > capRent {
				return errf(ErrRentAboveCap, "rent %d exceeds cap %d (current=%d lastAdjust=%d issue=%d)",
					newRent, capRent, l.rent, l.lastAdjust, now)
			}
		}
		l.offer = &Offer{ID: offerID, Rent: newRent, NewEnd: newEnd, IssueDay: now, Stage: StageTenant}
		l.offerEverIssued = true
		c.offers[offerID] = leaseID
		return nil
	})
}

func (m *naiveModel) withdrawOffer(offerID string, now int) error {
	return m.run(now, func(c *naiveModel) error {
		leaseID, ok := c.offers[offerID]
		if !ok {
			return errf(ErrLeaseNotFound, "offer not found: %q", offerID)
		}
		l := c.leases[leaseID]
		o := l.offer
		if o == nil || o.ID != offerID {
			return errf(ErrIllegalState, "offer %s is not the current offer", offerID)
		}
		if o.Stage != StageTenant {
			return errf(ErrIllegalState, "offer %s not pending tenant reply (stage=%s)", offerID, o.Stage)
		}
		if now > o.tenantDeadline(c.cfg.C) {
			return errf(ErrReplyLate, "offer %s reply deadline %d already passed", offerID, o.tenantDeadline(c.cfg.C))
		}
		o.Stage = StageWithdrawn
		o.ResolveDay = now
		return nil
	})
}

func (m *naiveModel) tenantReply(offerID string, now, counterRent int, accept bool) error {
	counter := counterRent > 0
	return m.run(now, func(c *naiveModel) error {
		leaseID, ok := c.offers[offerID]
		if !ok {
			return errf(ErrLeaseNotFound, "offer not found: %q", offerID)
		}
		l := c.leases[leaseID]
		o := l.offer
		if o == nil || o.ID != offerID {
			return errf(ErrIllegalState, "offer %s no longer available", offerID)
		}
		if l.phase != PhaseActive {
			return errf(ErrIllegalState, "lease %s phase %s forbids reply", leaseID, l.phase)
		}
		deadline := o.tenantDeadline(c.cfg.C)
		if o.Stage == StageExpired || now > deadline {
			return errf(ErrReplyLate, "offer %s late: now=%d deadline=%d", offerID, now, deadline)
		}
		if o.Stage != StageTenant {
			return errf(ErrIllegalState, "offer %s already in stage %s", offerID, o.Stage)
		}
		if counter {
			if !strictBetween(counterRent, l.rent, o.Rent) {
				return errf(ErrInvalidArgument, "counter rent %d must be strictly between %d and %d",
					counterRent, l.rent, o.Rent)
			}
			o.Stage = StageCounter
			o.CounterRent = counterRent
			o.CounterDay = now
			return nil
		}
		if accept {
			c.renew(l, o.Rent, o.NewEnd, now)
		} else {
			o.Stage = StageRejected
			o.ResolveDay = now
		}
		return nil
	})
}

func (m *naiveModel) landlordCounterReply(offerID string, now int, accept bool) error {
	return m.run(now, func(c *naiveModel) error {
		leaseID, ok := c.offers[offerID]
		if !ok {
			return errf(ErrLeaseNotFound, "offer not found: %q", offerID)
		}
		l := c.leases[leaseID]
		o := l.offer
		if o == nil || o.ID != offerID {
			return errf(ErrIllegalState, "offer %s no longer available", offerID)
		}
		if l.phase != PhaseActive {
			return errf(ErrIllegalState, "lease %s phase %s forbids counter reply", leaseID, l.phase)
		}
		if o.Stage != StageCounter {
			return errf(ErrIllegalState, "offer %s not awaiting landlord counter reply (stage=%s)",
				offerID, o.Stage)
		}
		deadline := o.counterDeadline(c.cfg.C)
		if now > deadline {
			return errf(ErrReplyLate, "counter %s late: now=%d deadline=%d", offerID, now, deadline)
		}
		if accept {
			c.renew(l, o.CounterRent, o.NewEnd, now)
		} else {
			o.Stage = StageRejected
			o.ResolveDay = now
		}
		return nil
	})
}

func (m *naiveModel) renew(l *lease, newRent, newEnd, resolveDay int) {
	o := l.offer
	o.Stage = StageAccepted
	o.ResolveDay = resolveDay
	l.start = l.end
	l.end = newEnd
	if newRent != l.rent {
		l.rent = newRent
		l.lastAdjust = l.start
	}
	l.offerEverIssued = false
	l.protected = false
	l.generation++
}

func (m *naiveModel) terminateHoldover(leaseID string, now int) error {
	return m.run(now, func(c *naiveModel) error {
		l, ok := c.leases[leaseID]
		if !ok {
			return errf(ErrLeaseNotFound, "lease not found: %q", leaseID)
		}
		if l.phase != PhaseHoldover {
			return errf(ErrIllegalState, "lease %s phase %s is not holdover", leaseID, l.phase)
		}
		if l.noticeDay != 0 {
			return errf(ErrIllegalState, "lease %s already has a termination notice", leaseID)
		}
		l.noticeDay = now
		l.terminateAt = now + c.cfg.D
		return nil
	})
}

// statusView 推进临时副本到 now 并取视图，不改变模型自身状态。
func (m *naiveModel) statusView(leaseID string, now int) (*LeaseView, error) {
	c, err := m.try(now, func(c *naiveModel) error { return nil })
	if err != nil {
		return nil, err
	}
	l, ok := c.leases[leaseID]
	if !ok {
		return nil, errf(ErrLeaseNotFound, "lease not found: %q", leaseID)
	}
	v := &LeaseView{
		ID: l.id, Start: l.start, End: l.end, Rent: l.rent, LastAdjust: l.lastAdjust,
		Phase: l.phase, Protected: l.protected, HoldoverStart: l.holdoverStart,
		NoticeDay: l.noticeDay, TerminateAt: l.terminateAt,
	}
	if l.offer != nil {
		switch l.offer.Stage {
		case StageTenant, StageCounter:
			v.PendingOffer = offerView(l.offer)
		default:
			v.LastOffer = offerView(l.offer)
		}
	}
	return v, nil
}
