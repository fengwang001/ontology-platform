package ontology

import (
	"sort"
	"sync"
)

// Service 是续签与涨幅管制服务。所有方法并发安全：全局锁串行化，
// 可线性化，因此不可能出现同一租约两份未决要约或同一要约被接受两次。
type Service struct {
	mu     sync.Mutex
	cfg    Config
	sch    *scheduler
	leases map[string]*lease
	offers map[string]string // offerID -> leaseID
	day    int               // 上一次“被接受操作”的 now
}

// NewService 构造服务；A、B、C、D 及档位非法时报参数错误。
func NewService(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, sch: newScheduler(), leases: map[string]*lease{}, offers: map[string]string{}}, nil
}

func strictBetween(x, lo, hi int) bool {
	if lo > hi {
		lo, hi = hi, lo
	}
	return x > lo && x < hi
}

// LastAcceptedDay 返回上一次被接受操作携带的 now。
func (s *Service) LastAcceptedDay() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.day
}

var kindRank = map[byte]int{
	kindProtection:  0,
	kindTenantLate:  1,
	kindCounterLate: 2,
	kindEnd:         3,
	kindTerminate:   4,
}

// txn 在锁内执行“临时推进到期事件 → 校验并执行操作 → 提交/回滚”。
// 事件先应用到受影响租约的副本上；body 返回错误时恢复全部副本并把
// 弹出的事件放回堆——被拒绝操作不改变任何租约/要约状态与时钟。
// 弹出条目数等于截至 now 的真实到期事件数（加少量续签作废的陈旧条目），
// 与租约总数无关。
func (s *Service) txn(now int, body func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.day {
		return errf(ErrClockRollback, "clock rollback: now=%d < last accepted now=%d", now, s.day)
	}

	popped := s.sch.peekPop(now)
	sort.SliceStable(popped, func(i, j int) bool {
		if popped[i].at != popped[j].at {
			return popped[i].at < popped[j].at
		}
		return kindRank[popped[i].kind] < kindRank[popped[j].kind]
	})

	saved := map[*lease]lease{}
	save := func(l *lease) {
		if _, ok := saved[l]; !ok {
			old := *l
			if old.offer != nil {
				oc := *old.offer
				old.offer = &oc
			}
			saved[l] = old
		}
	}

	var applied []*schedEntry
	for _, e := range popped {
		l, ok := s.leases[e.leaseID]
		if !ok || l.generation != e.generation || !s.validEvent(e, l, now) {
			continue
		}
		save(l)
		s.applyEvent(e.kind, l, e.at)
		applied = append(applied, e)
	}

	if err := body(); err != nil {
		for l, old := range saved {
			*l = old
		}
		s.sch.restore(popped)
		return err
	}

	for _, e := range popped {
		if l, ok := s.leases[e.leaseID]; ok && l.generation == e.generation && s.validEvent(e, l, now) {
			s.sch.pushRaw(e) // 已应用事件不会满足 validEvent；防御性放回
		}
	}
	s.day = now
	return nil
}

func (s *Service) validEvent(e *schedEntry, l *lease, now int) bool {
	switch e.kind {
	case kindProtection:
		return l.phase == PhaseActive && !l.protected && !l.offerEverIssued
	case kindEnd:
		return l.phase == PhaseActive
	case kindTenantLate:
		return l.offer != nil && l.offer.ID == e.offerID && l.offer.Stage == StageTenant
	case kindCounterLate:
		return l.offer != nil && l.offer.ID == e.offerID && l.offer.Stage == StageCounter
	case kindTerminate:
		return l.phase == PhaseHoldover
	}
	return false
}

func (s *Service) applyEvent(kind byte, l *lease, at int) {
	switch kind {
	case kindProtection:
		l.protected = true
	case kindTenantLate, kindCounterLate:
		l.offer.Stage = StageExpired
		l.offer.ResolveDay = at // 事件排在截止日次日，即逾期起始日
		if at >= l.end {
			s.enterHoldover(l)
		}
	case kindEnd:
		if l.protected || !l.offerEverIssued || (l.offer != nil && l.offer.Stage == StageExpired) {
			s.enterHoldover(l)
		} else {
			// 已被拒绝/撤回：不延续；未决且未逾期：等待逾期事件。
			l.phase = PhaseEnded
			l.generation++
		}
	case kindTerminate:
		l.phase = PhaseTerminated
		l.generation++
	}
}

func (s *Service) enterHoldover(l *lease) {
	l.phase = PhaseHoldover
	l.holdoverStart = l.end
	l.protected = true
	if l.offer != nil && (l.offer.Stage == StageTenant || l.offer.Stage == StageCounter) {
		l.offer.Stage = StageExpired
		l.offer.ResolveDay = l.end
	}
	l.generation++
}

func (s *Service) scheduleLease(l *lease) {
	s.sch.push(l.end-s.cfg.A+1, l.id, l.generation, kindProtection, "")
	s.sch.push(l.end, l.id, l.generation, kindEnd, "")
}

// AddLease 登记一份租约。日序号为整数，[start, end) 在租。
func (s *Service) AddLease(id string, start, end, rent, lastAdjust, now int) error {
	return s.txn(now, func() error {
		if id == "" || start < 0 || end <= start || rent <= 0 ||
			lastAdjust > now {
			return errf(ErrInvalidArgument, "invalid lease: id=%q start=%d end=%d rent=%d lastAdjust=%d",
				id, start, end, rent, lastAdjust)
		}
		if _, ok := s.leases[id]; ok {
			return errf(ErrInvalidArgument, "lease id duplicated: %q", id)
		}
		l := &lease{
			id: id, start: start, end: end, rent: rent, lastAdjust: lastAdjust,
			phase: PhaseActive, generation: 1,
		}
		s.leases[id] = l
		s.scheduleLease(l)
		return nil
	})
}

// IssueOffer 由房东在续签窗口内发出要约（窗口两端取等均可）。
func (s *Service) IssueOffer(leaseID, offerID string, newRent, newEnd, now int) error {
	if offerID == "" || newRent <= 0 || newEnd <= 0 {
		return errf(ErrInvalidArgument, "invalid offer: id=%q rent=%d end=%d", offerID, newRent, newEnd)
	}
	return s.txn(now, func() error {
		l, ok := s.leases[leaseID]
		if !ok {
			return errf(ErrLeaseNotFound, "lease not found: %q", leaseID)
		}
		if _, dup := s.offers[offerID]; dup {
			return errf(ErrInvalidArgument, "offer id duplicated: %q", offerID)
		}
		if l.phase != PhaseActive || l.protected {
			return errf(ErrIllegalState, "lease %s not open to offer (phase=%s protected=%v)",
				leaseID, l.phase, l.protected)
		}
		if l.offer != nil && (l.offer.Stage == StageTenant || l.offer.Stage == StageCounter) {
			return errf(ErrIllegalState, "lease %s already has pending offer %s", leaseID, l.offer.ID)
		}
		left, right := l.end-s.cfg.B, l.end-s.cfg.A
		if now < left || now > right {
			return errf(ErrIllegalState, "issue day %d outside renewal window [%d,%d] (end=%d)",
				now, left, right, l.end)
		}
		if newEnd <= l.end {
			return errf(ErrInvalidArgument, "new end %d must extend beyond current end %d", newEnd, l.end)
		}
		if newRent > l.rent {
			capRent := rentCap(l.rent, l.lastAdjust, now, s.cfg.StepBP, s.cfg.CapBP)
			if newRent > capRent {
				return errf(ErrRentAboveCap, "rent %d exceeds cap %d (current=%d lastAdjust=%d issue=%d)",
					newRent, capRent, l.rent, l.lastAdjust, now)
			}
		}
		l.offer = &Offer{ID: offerID, Rent: newRent, NewEnd: newEnd, IssueDay: now, Stage: StageTenant}
		l.offerEverIssued = true
		s.offers[offerID] = leaseID
		s.sch.push(now+s.cfg.C+1, leaseID, l.generation, kindTenantLate, l.offer.ID)
		return nil
	})
}

// WithdrawOffer 由房东撤回等待租户答复的未决要约。
func (s *Service) WithdrawOffer(offerID string, now int) error {
	return s.txn(now, func() error {
		leaseID, ok := s.offers[offerID]
		if !ok {
			return errf(ErrLeaseNotFound, "offer not found: %q", offerID)
		}
		l := s.leases[leaseID]
		o := l.offer
		if o == nil || o.ID != offerID {
			return errf(ErrIllegalState, "offer %s is not the current offer", offerID)
		}
		if o.Stage != StageTenant {
			return errf(ErrIllegalState, "offer %s not pending tenant reply (stage=%s)", offerID, o.Stage)
		}
		if now > o.tenantDeadline(s.cfg.C) {
			return errf(ErrReplyLate, "offer %s reply deadline %d already passed", offerID, o.tenantDeadline(s.cfg.C))
		}
		o.Stage = StageWithdrawn
		o.ResolveDay = now
		return nil
	})
}

// TenantReply 为租户答复：accept=true 接受；counterRent>0 提出反要约；
// 两者都不为真即为拒绝。
func (s *Service) TenantReply(offerID string, now, counterRent int, accept bool) error {
	counter := counterRent > 0
	return s.txn(now, func() error {
		leaseID, ok := s.offers[offerID]
		if !ok {
			return errf(ErrLeaseNotFound, "offer not found: %q", offerID)
		}
		l := s.leases[leaseID]
		o := l.offer
		if o == nil || o.ID != offerID {
			return errf(ErrIllegalState, "offer %s no longer available", offerID)
		}
		if l.phase != PhaseActive {
			return errf(ErrIllegalState, "lease %s phase %s forbids reply", leaseID, l.phase)
		}
		deadline := o.tenantDeadline(s.cfg.C)
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
			s.sch.push(now+s.cfg.C+1, leaseID, l.generation, kindCounterLate, l.offer.ID)
			return nil
		}
		if accept {
			s.renew(l, o.Rent, o.NewEnd, now)
		} else {
			o.Stage = StageRejected
			o.ResolveDay = now
		}
		return nil
	})
}

// LandlordCounterReply 为房东对反要约的答复（答复期从反要约提出日起 C 天）。
func (s *Service) LandlordCounterReply(offerID string, now int, accept bool) error {
	return s.txn(now, func() error {
		leaseID, ok := s.offers[offerID]
		if !ok {
			return errf(ErrLeaseNotFound, "offer not found: %q", offerID)
		}
		l := s.leases[leaseID]
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
		deadline := o.counterDeadline(s.cfg.C)
		if now > deadline {
			return errf(ErrReplyLate, "counter %s late: now=%d deadline=%d", offerID, now, deadline)
		}
		if accept {
			s.renew(l, o.CounterRent, o.NewEnd, now)
		} else {
			o.Stage = StageRejected
			o.ResolveDay = now
		}
		return nil
	})
}

func (s *Service) renew(l *lease, newRent, newEnd, resolveDay int) {
	o := l.offer
	o.Stage = StageAccepted
	o.ResolveDay = resolveDay
	l.start = l.end
	l.end = newEnd
	if newRent != l.rent {
		l.rent = newRent
		l.lastAdjust = l.start // 新租金自新租期起始日（旧终止日）生效
	}
	l.offerEverIssued = false
	l.protected = false
	l.generation++
	s.scheduleLease(l)
}

// TerminateHoldover 在按月延续期内发出终止通知，通知日之后第 D 天生效。
func (s *Service) TerminateHoldover(leaseID string, now int) error {
	return s.txn(now, func() error {
		l, ok := s.leases[leaseID]
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
		l.terminateAt = now + s.cfg.D
		s.sch.push(l.terminateAt, leaseID, l.generation, kindTerminate, "")
		return nil
	})
}

// Status 返回租约在 now 下应有的只读状态。查询不推进已提交时钟；
// 为保证重放确定，查询的 now 同样不得小于上一次被接受操作的 now。
func (s *Service) Status(leaseID string, now int) (*LeaseView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.day {
		return nil, errf(ErrClockRollback, "clock rollback: now=%d < last accepted now=%d", now, s.day)
	}
	l, ok := s.leases[leaseID]
	if !ok {
		return nil, errf(ErrLeaseNotFound, "lease not found: %q", leaseID)
	}
	popped := s.sch.peekPop(now)
	sort.SliceStable(popped, func(i, j int) bool {
		if popped[i].at != popped[j].at {
			return popped[i].at < popped[j].at
		}
		return kindRank[popped[i].kind] < kindRank[popped[j].kind]
	})
	touched := map[*lease]lease{}
	save := func(l *lease) {
		if _, ok := touched[l]; !ok {
			old := *l
			if old.offer != nil {
				oc := *old.offer
				old.offer = &oc
			}
			touched[l] = old
		}
	}
	for _, e := range popped {
		le, ok := s.leases[e.leaseID]
		if !ok || le.generation != e.generation || !s.validEvent(e, le, now) {
			continue
		}
		save(le)
		s.applyEvent(e.kind, le, e.at)
	}
	v := s.viewLocked(l)
	for le, old := range touched {
		*le = old
	}
	s.sch.restore(popped)
	return v, nil
}
