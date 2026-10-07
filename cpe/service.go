package cpe

import (
	"fmt"
	"sync"
)

// OpEvent 记录一次操作调用的完整输入与结果，用于审计、重放与并发等价验证。
type OpEvent struct {
	Seq            int
	Op             string // RegisterHolder / RegisterCredit / CorrectCredit / RevokeCredit
	Now            int
	HolderID       string
	RecordID       string
	Org            string
	Category       Category
	Credits        int
	EarnedDate     int
	IssueDate      int
	NewCredits     int
	Err            ErrKind
	ResultRecordID string
}

// Service 为继续教育学分周期核算服务。所有方法可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Service struct {
	mu         sync.Mutex
	cfg        Config
	lastNow    int
	hasLastNow bool
	holders    map[string]*holder
	seq        int
	nextRecID  int
	log        []OpEvent
}

// NewService 以服务级配置创建核算服务。
func NewService(cfg Config) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, holders: map[string]*holder{}}, nil
}

// LastNow 返回当前逻辑时钟（最后一次被接受操作的 now）。
func (s *Service) LastNow() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastNow, s.hasLastNow
}

// AuditLog 返回全部操作（含被拒绝）的审计日志副本。
func (s *Service) AuditLog() []OpEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OpEvent, len(s.log))
	copy(out, s.log)
	return out
}

func (s *Service) logEvent(ev OpEvent) {
	ev.Seq = s.seq
	s.seq++
	s.log = append(s.log, ev)
}

// checkClock 校验时钟；通过后（操作被接受）由调用方提交。
func (s *Service) checkClock(now int) error {
	if s.hasLastNow && now < s.lastNow {
		return newErr(ErrClockRollback, fmt.Sprintf("now %d < last accepted now %d", now, s.lastNow))
	}
	return nil
}

func (s *Service) commitClock(now int) {
	s.lastNow = now
	s.hasLastNow = true
}

// RegisterHolder 注册持证人（发证日为首个周期起点）；证书失效后可重新注册为新持证人。
func (s *Service) RegisterHolder(holderID string, issueDate int, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := OpEvent{Op: "RegisterHolder", Now: now, HolderID: holderID, IssueDate: issueDate}
	defer func() { s.logEvent(ev) }()

	if holderID == "" || issueDate < 0 || now < 0 {
		ev.Err = ErrInvalidParam
		return newErr(ErrInvalidParam, "holderID must be non-empty, issueDate/now must be >= 0")
	}
	if err := s.checkClock(now); err != nil {
		ev.Err = ErrClockRollback
		return err
	}
	if h, ok := s.holders[holderID]; ok {
		d := h.derive()
		d.advanceTo(now, s.cfg)
		if d.status == CertActive {
			ev.Err = ErrStateNotAllowed
			return newErr(ErrStateNotAllowed, "holder already registered and active")
		}
		// 已失效：重新注册为新持证人，原学分不继承。
		h.advanceTo(now, s.cfg)
		h.generation++
		h.resetLife(issueDate, s.cfg)
		h.snapshot(now)
		s.commitClock(now)
		return nil
	}
	h := newHolder(holderID, issueDate, s.cfg)
	s.holders[holderID] = h
	h.snapshot(now)
	s.commitClock(now)
	return nil
}

// RegisterCredit 登记一条学分记录，返回记录 ID。
func (s *Service) RegisterCredit(holderID string, cat Category, credits int, earnedDate int, org string, now int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := OpEvent{Op: "RegisterCredit", Now: now, HolderID: holderID, Org: org, Category: cat, Credits: credits, EarnedDate: earnedDate}
	defer func() { s.logEvent(ev) }()

	fail := func(kind ErrKind, err error) (string, error) {
		ev.Err = kind
		return "", err
	}

	if holderID == "" || org == "" || !cat.valid() || credits <= 0 || earnedDate < 0 || now < 0 {
		return fail(ErrInvalidParam, newErr(ErrInvalidParam, "invalid register-credit arguments"))
	}
	if earnedDate > now {
		return fail(ErrInvalidParam, newErr(ErrInvalidParam, "earnedDate must not be later than now"))
	}
	if err := s.checkClock(now); err != nil {
		return fail(ErrClockRollback, err)
	}
	h, ok := s.holders[holderID]
	if !ok {
		return fail(ErrNotFound, newErr(ErrNotFound, "holder not found"))
	}
	key := dedupKey(org, earnedDate, cat)
	if _, dup := h.dedup[key]; dup {
		return fail(ErrDuplicate, newErr(ErrDuplicate, "duplicate registration for same org/date/category"))
	}
	d := h.derive()
	d.advanceTo(now, s.cfg)
	if d.status == CertExpired {
		return fail(ErrCertificateExpired, newErr(ErrCertificateExpired, "certificate expired"))
	}

	h.advanceTo(now, s.cfg)
	recID := fmt.Sprintf("R%08d", s.nextRecID)
	s.nextRecID++
	rec := &record{
		id:            recID,
		category:      cat,
		credits:       credits,
		earnedDate:    earnedDate,
		org:           org,
		registeredNow: now,
		cycle:         h.attribute(earnedDate, now, s.cfg),
	}
	if rec.cycle >= 0 {
		h.cycleByIndex(rec.cycle).tally.add(cat, credits)
	}
	h.records[recID] = rec
	h.dedup[key] = recID
	h.refreshGracePass(s.cfg)
	h.snapshot(now)
	s.commitClock(now)
	ev.ResultRecordID = recID
	return recID, nil
}

// CorrectCredit 由出具机构更正记录的学分数。
func (s *Service) CorrectCredit(holderID, recordID, org string, newCredits int, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := OpEvent{Op: "CorrectCredit", Now: now, HolderID: holderID, RecordID: recordID, Org: org, NewCredits: newCredits}
	defer func() { s.logEvent(ev) }()

	fail := func(kind ErrKind, err error) error {
		ev.Err = kind
		return err
	}

	if holderID == "" || recordID == "" || org == "" || newCredits <= 0 || now < 0 {
		return fail(ErrInvalidParam, newErr(ErrInvalidParam, "invalid correct-credit arguments"))
	}
	if err := s.checkClock(now); err != nil {
		return fail(ErrClockRollback, err)
	}
	h, ok := s.holders[holderID]
	if !ok {
		return fail(ErrNotFound, newErr(ErrNotFound, "holder not found"))
	}
	rec, ok := h.records[recordID]
	if !ok {
		return fail(ErrNotFound, newErr(ErrNotFound, "record not found"))
	}
	if rec.revoked {
		return fail(ErrStateNotAllowed, newErr(ErrStateNotAllowed, "record already revoked"))
	}
	if rec.org != org {
		return fail(ErrStateNotAllowed, newErr(ErrStateNotAllowed, "only the issuing org may correct the record"))
	}
	if now-rec.registeredNow > s.cfg.CorrectionWindowDays {
		return fail(ErrCorrectionWindowExpired, newErr(ErrCorrectionWindowExpired, "correction window exceeded"))
	}
	d := h.derive()
	d.advanceTo(now, s.cfg)
	if d.status == CertExpired {
		return fail(ErrCertificateExpired, newErr(ErrCertificateExpired, "certificate expired"))
	}

	h.advanceTo(now, s.cfg)
	delta := newCredits - rec.credits
	rec.credits = newCredits
	if rec.cycle >= 0 {
		if c := h.cycleByIndex(rec.cycle); c != nil && !c.closed {
			c.tally.add(rec.category, delta)
		}
	}
	h.refreshGracePass(s.cfg)
	h.snapshot(now)
	s.commitClock(now)
	return nil
}

// RevokeCredit 由出具机构撤销记录。
func (s *Service) RevokeCredit(holderID, recordID, org string, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := OpEvent{Op: "RevokeCredit", Now: now, HolderID: holderID, RecordID: recordID, Org: org}
	defer func() { s.logEvent(ev) }()

	fail := func(kind ErrKind, err error) error {
		ev.Err = kind
		return err
	}

	if holderID == "" || recordID == "" || org == "" || now < 0 {
		return fail(ErrInvalidParam, newErr(ErrInvalidParam, "invalid revoke-credit arguments"))
	}
	if err := s.checkClock(now); err != nil {
		return fail(ErrClockRollback, err)
	}
	h, ok := s.holders[holderID]
	if !ok {
		return fail(ErrNotFound, newErr(ErrNotFound, "holder not found"))
	}
	rec, ok := h.records[recordID]
	if !ok {
		return fail(ErrNotFound, newErr(ErrNotFound, "record not found"))
	}
	if rec.revoked {
		return fail(ErrStateNotAllowed, newErr(ErrStateNotAllowed, "record already revoked"))
	}
	if rec.org != org {
		return fail(ErrStateNotAllowed, newErr(ErrStateNotAllowed, "only the issuing org may revoke the record"))
	}
	if now-rec.registeredNow > s.cfg.CorrectionWindowDays {
		return fail(ErrCorrectionWindowExpired, newErr(ErrCorrectionWindowExpired, "correction window exceeded"))
	}
	d := h.derive()
	d.advanceTo(now, s.cfg)
	if d.status == CertExpired {
		return fail(ErrCertificateExpired, newErr(ErrCertificateExpired, "certificate expired"))
	}

	h.advanceTo(now, s.cfg)
	rec.revoked = true
	delete(h.dedup, dedupKey(rec.org, rec.earnedDate, rec.category))
	if rec.cycle >= 0 {
		if c := h.cycleByIndex(rec.cycle); c != nil && !c.closed {
			c.tally.add(rec.category, -rec.credits)
		}
	}
	h.refreshGracePass(s.cfg)
	h.snapshot(now)
	s.commitClock(now)
	return nil
}

// GetAccounting 返回当前时刻（最后被接受操作的 now）的核算视图。
func (s *Service) GetAccounting(holderID string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.holders[holderID]
	if !ok {
		return View{}, newErr(ErrNotFound, "holder not found")
	}
	d := h.derive()
	d.advanceTo(s.lastNow, s.cfg)
	return d.buildView(s.lastNow, s.cfg), nil
}

// GetAccountingAt 查询任意历史时刻（asOf 不超过当前时钟）的核算视图，
// 结果与当时的实际核算一致。
func (s *Service) GetAccountingAt(holderID string, asOf int) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if asOf < 0 || (s.hasLastNow && asOf > s.lastNow) {
		return View{}, newErr(ErrInvalidParam, "asOf out of range")
	}
	h, ok := s.holders[holderID]
	if !ok {
		return View{}, newErr(ErrNotFound, "holder not found")
	}
	// 二分查找最后一个 now <= asOf 的快照。
	lo, hi := 0, len(h.snaps)
	for lo < hi {
		mid := (lo + hi) / 2
		if h.snaps[mid].now <= asOf {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return View{}, newErr(ErrNotFound, "holder did not exist at asOf")
	}
	snap := h.snaps[lo-1]
	return snap.viewAt(h, asOf, s.cfg), nil
}
