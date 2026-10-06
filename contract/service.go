package contract

import (
	"fmt"
	"sync"
)

// amendment 内部的补充协议状态。
type amendment struct {
	id            string
	effectiveDecl int64 // 声明生效日
	changes       map[string]int
	revokes       string // 非空：撤销目标协议
	needsCosign   bool   // 是否涉及锁定条款
	signatures    map[string]bool
	signatureDay  map[string]int64
	completedDay  int64 // 双方签署完成日；-1 表示未完成
	completionSeq int   // 完成时刻（全局序号）；-1
	cosignedAt    int64 // 法务会签时刻；-1
	effectiveDay  int64 // 实际生效日；-1
	createdSeq    int
}

// terminationAgreement 双方协商提前终止的过程记录。
type terminationAgreement struct {
	requesters map[string]bool
	day        int64
	seq        int
}

type contractState struct {
	id          string
	parties     [2]PartyInput
	clauses     map[string]int
	locked      map[string]bool
	startDay    int64
	expiryDay   int64
	amendments  map[string]*amendment
	order       []*amendment // 按创建顺序
	notices     []noticeRecord
	termination *terminationAgreement
	termDay     int64 // 实际终止日（到期终止或提前终止）；-1
	globalSeq   int

	// 每条款的候选协议索引（仅包含已生效的修改协议）。
	byClause map[string][]*amendment
	// 撤销关系索引：target -> 撤销它的协议（查询时按生效日过滤）。
	revokerChildren map[string][]*amendment
}

type noticeRecord struct {
	partyID string
	day     int64
	seq     int
}

// Service 合同版本叠加与自动续签服务。所有方法可并发调用。
type Service struct {
	mu        sync.Mutex
	contracts map[string]*contractState
	seq       int
	lastNow   int64
	clockSet  bool
	logs      []OperationLog
	logging   bool
}

// NewService 创建空服务。
func NewService() *Service {
	return &Service{contracts: map[string]*contractState{}, seq: 0}
}

func (c *contractState) nextSeq() int {
	c.globalSeq++
	return c.globalSeq
}

func (s *Service) checkClock(now int64) error {
	if s.clockSet && now < s.lastNow {
		return errf(ErrClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	return nil
}

func (s *Service) advanceClock(now int64) {
	s.lastNow = now
	s.clockSet = true
}

// CreateContract 创建主合同。
func (s *Service) CreateContract(in CreateContractInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateCreate(in); err != nil {
		return err
	}
	if err := s.checkClock(in.Now); err != nil {
		return err
	}
	if _, exists := s.contracts[in.ID]; exists {
		return errf(ErrIllegalState, "contract %q already exists", in.ID)
	}
	c := &contractState{
		id:              in.ID,
		parties:         in.Parties,
		clauses:         cloneClauses(in.Clauses),
		locked:          cloneLocked(in.LockedClauses),
		startDay:        in.StartDay,
		expiryDay:       in.ExpiryDay,
		amendments:      map[string]*amendment{},
		termDay:         -1,
		globalSeq:       0,
		byClause:        map[string][]*amendment{},
		revokerChildren: map[string][]*amendment{},
	}
	s.contracts[in.ID] = c
	s.advanceClock(in.Now)
	s.record(c, "CreateContract", fmt.Sprintf("id=%s now=%d", in.ID, in.Now), "OK", "主合同已创建")
	return nil
}

// AddAmendment 增加一份补充协议（尚未签署）。
func (s *Service) AddAmendment(in AddAmendmentInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateAdd(in); err != nil {
		return err
	}
	if err := s.checkClock(in.Now); err != nil {
		return err
	}
	c := s.contracts[in.ContractID]
	if c == nil {
		return errf(ErrNotFound, "contract %q not found", in.ContractID)
	}
	if c.terminatedAt() >= 0 {
		return errf(ErrIllegalState, "contract already terminated on day %d", c.terminatedAt())
	}
	if _, exists := c.amendments[in.AmendmentID]; exists {
		return errf(ErrIllegalState, "amendment %q already exists", in.AmendmentID)
	}
	if in.Revokes != "" {
		target := c.amendments[in.Revokes]
		if target == nil {
			return errf(ErrNotFound, "revocation target %q not found", in.Revokes)
		}
		if target.revokes != "" {
			// 允许撤销撤销协议，所以这里不限制；仅记录。
		}
	}
	needs := false
	for cid := range in.Changes {
		if _, ok := c.clauses[cid]; !ok {
			return errf(ErrInvalidParam, "clause %q not present in main contract", cid)
		}
		if c.locked[cid] {
			needs = true
		}
	}
	a := &amendment{
		id:            in.AmendmentID,
		effectiveDecl: in.EffectiveDay,
		changes:       cloneClauses(in.Changes),
		revokes:       in.Revokes,
		needsCosign:   needs,
		signatures:    map[string]bool{},
		signatureDay:  map[string]int64{},
		completedDay:  -1,
		completionSeq: -1,
		cosignedAt:    -1,
		effectiveDay:  -1,
		createdSeq:    c.nextSeq(),
	}
	c.amendments[a.id] = a
	c.order = append(c.order, a)
	s.advanceClock(in.Now)
	reason := "条款修改协议待签署"
	if a.revokes != "" {
		reason = fmt.Sprintf("撤销协议（目标 %s）待签署", a.revokes)
	}
	if needs {
		reason += "；含锁定条款需法务会签"
	}
	s.record(c, "AddAmendment",
		fmt.Sprintf("contract=%s amendment=%s now=%d effective>=%d changes=%v revokes=%s",
			in.ContractID, in.AmendmentID, in.Now, in.EffectiveDay, in.Changes, in.Revokes),
		"OK", reason)
	return nil
}

// Sign 某方在 now 日签署指定协议。
func (s *Service) Sign(contractID, amendmentID, partyID string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isBlank(contractID) || isBlank(amendmentID) || isBlank(partyID) {
		return errf(ErrInvalidParam, "blank id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c := s.contracts[contractID]
	if c == nil {
		return errf(ErrNotFound, "contract %q not found", contractID)
	}
	a := c.amendments[amendmentID]
	if a == nil {
		return errf(ErrNotFound, "amendment %q not found", amendmentID)
	}
	if c.terminatedAt() >= 0 {
		return errf(ErrIllegalState, "contract already terminated")
	}
	pIdx := c.partyIndex(partyID)
	if pIdx < 0 {
		return errf(ErrInvalidParam, "party %q is not a contract party", partyID)
	}
	if a.completedDay >= 0 {
		return errf(ErrIllegalState, "amendment already fully signed")
	}
	if a.signatures[partyID] {
		return errf(ErrIllegalState, "party already signed")
	}
	// 授权窗口（含两端）。
	p := c.parties[pIdx]
	if now < p.AuthFrom || now > p.AuthUntil {
		return errf(ErrAuthExpired, "party %q not authorized on day %d (window [%d,%d])",
			partyID, now, p.AuthFrom, p.AuthUntil)
	}
	// 签署超期：若已有一方签署，以其签署日为首签日；
	// now == first + deadline 仍可签，严格大于才超期。
	if len(a.signatures) > 0 {
		var firstDay int64 = now
		for _, d := range a.signatureDay {
			if d < firstDay {
				firstDay = d
			}
		}
		if now > firstDay+signingDeadlineDays {
			return errf(ErrSigningExpired, "signing window expired: now %d > first %d + %d",
				now, firstDay, signingDeadlineDays)
		}
	}
	a.signatures[partyID] = true
	a.signatureDay[partyID] = now
	if len(a.signatures) == 2 {
		a.completedDay = now
		a.completionSeq = c.nextSeq()
		c.tryActivate(a)
	}
	s.advanceClock(now)
	reason := fmt.Sprintf("party=%s 签署成功，已签=%d/2", partyID, len(a.signatures))
	if a.completedDay >= 0 {
		reason += fmt.Sprintf("；签署完成日=%d", a.completedDay)
		if a.effectiveDay >= 0 {
			reason += fmt.Sprintf("；协议生效日=%d", a.effectiveDay)
		} else if a.needsCosign {
			reason += "；等待法务会签"
		}
	}
	s.record(c, "Sign",
		fmt.Sprintf("contract=%s amendment=%s party=%s now=%d", contractID, amendmentID, partyID, now),
		"OK", reason)
	return nil
}

// LegalCosign 法务在 now 日对协议补会签。
func (s *Service) LegalCosign(contractID, amendmentID string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isBlank(contractID) || isBlank(amendmentID) {
		return errf(ErrInvalidParam, "blank id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c := s.contracts[contractID]
	if c == nil {
		return errf(ErrNotFound, "contract %q not found", contractID)
	}
	a := c.amendments[amendmentID]
	if a == nil {
		return errf(ErrNotFound, "amendment %q not found", amendmentID)
	}
	if c.terminatedAt() >= 0 {
		return errf(ErrIllegalState, "contract already terminated")
	}
	if !a.needsCosign {
		return errf(ErrIllegalState, "amendment does not involve locked clauses")
	}
	if a.cosignedAt >= 0 {
		return errf(ErrIllegalState, "already cosigned")
	}
	a.cosignedAt = now
	c.tryActivate(a)
	s.advanceClock(now)
	reason := "法务会签已记录"
	if a.completedDay >= 0 {
		if a.effectiveDay >= 0 {
			reason += fmt.Sprintf("；协议生效日=%d", a.effectiveDay)
		}
	} else {
		reason += "；等待双方签署完成"
	}
	s.record(c, "LegalCosign",
		fmt.Sprintf("contract=%s amendment=%s now=%d", contractID, amendmentID, now),
		"OK", reason)
	return nil
}

// NoticeNonRenewal 某方在 now 日发出不续签通知。
func (s *Service) NoticeNonRenewal(contractID, partyID string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isBlank(contractID) || isBlank(partyID) {
		return errf(ErrInvalidParam, "blank id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c := s.contracts[contractID]
	if c == nil {
		return errf(ErrNotFound, "contract %q not found", contractID)
	}
	if c.partyIndex(partyID) < 0 {
		return errf(ErrInvalidParam, "party %q is not a contract party", partyID)
	}
	if c.terminatedAt() >= 0 {
		return errf(ErrIllegalState, "contract already terminated")
	}
	c.notices = append(c.notices, noticeRecord{partyID: partyID, day: now, seq: c.nextSeq()})
	s.advanceClock(now)
	s.record(c, "NoticeNonRenewal",
		fmt.Sprintf("contract=%s party=%s now=%d", contractID, partyID, now),
		"OK", "不续签通知已接受（是否及时在各到期日判定）")
	return nil
}

// AgreeEarlyTermination 某方同意在 now 日提前终止；双方均同意则当日终止。
func (s *Service) AgreeEarlyTermination(contractID, partyID string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isBlank(contractID) || isBlank(partyID) {
		return errf(ErrInvalidParam, "blank id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c := s.contracts[contractID]
	if c == nil {
		return errf(ErrNotFound, "contract %q not found", contractID)
	}
	if c.partyIndex(partyID) < 0 {
		return errf(ErrInvalidParam, "party %q is not a contract party", partyID)
	}
	p := c.parties[c.partyIndex(partyID)]
	if now < p.AuthFrom || now > p.AuthUntil {
		return errf(ErrAuthExpired, "party %q not authorized on day %d", partyID, now)
	}
	if c.termination == nil {
		c.termination = &terminationAgreement{requesters: map[string]bool{}, day: now, seq: c.nextSeq()}
	}
	if c.termination.requesters[partyID] {
		return errf(ErrIllegalState, "party already agreed to early termination")
	}
	c.termination.requesters[partyID] = true
	s.advanceClock(now)
	reason := "提前终止意向已记录"
	if len(c.termination.requesters) == 2 {
		c.termDay = now
		reason = fmt.Sprintf("双方同意，合同于 %d 日提前终止", now)
	}
	s.record(c, "AgreeEarlyTermination",
		fmt.Sprintf("contract=%s party=%s now=%d", contractID, partyID, now),
		"OK", reason)
	return nil
}

// EffectiveValue 查询 day 日某条款的有效值及来源。
func (s *Service) EffectiveValue(contractID, clauseID string, day int64) (ValueSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contracts[contractID]
	if c == nil {
		return ValueSource{}, errf(ErrNotFound, "contract %q not found", contractID)
	}
	mainVal, ok := c.clauses[clauseID]
	if !ok {
		return ValueSource{}, errf(ErrNotFound, "clause %q not found", clauseID)
	}
	src := effectiveValueAt(clauseID, mainVal, true, c.byClause[clauseID], c.revokerChildren, day)
	return src, nil
}

// InTerm 查询 day 日合同是否在期。
func (s *Service) InTerm(contractID string, day int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contracts[contractID]
	if c == nil {
		return false, errf(ErrNotFound, "contract %q not found", contractID)
	}
	return c.simulate(day).inTerm, nil
}

// CurrentExpiry 查询从 day 日观察到的到期日。
func (s *Service) CurrentExpiry(contractID string, day int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contracts[contractID]
	if c == nil {
		return 0, errf(ErrNotFound, "contract %q not found", contractID)
	}
	return c.simulate(day).currentExpiry, nil
}

// Renewals 查询截至 day 日的全部续签记录。
func (s *Service) Renewals(contractID string, day int64) ([]RenewalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contracts[contractID]
	if c == nil {
		return nil, errf(ErrNotFound, "contract %q not found", contractID)
	}
	st := c.simulate(day)
	out := make([]RenewalRecord, len(st.records))
	copy(out, st.records)
	return out, nil
}

// Logs 返回操作日志副本。
func (s *Service) Logs() []OperationLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OperationLog, len(s.logs))
	copy(out, s.logs)
	return out
}

// SetLogging 开关逐步操作日志。
func (s *Service) SetLogging(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logging = on
}
