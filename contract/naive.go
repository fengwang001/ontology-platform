package contract

// 本文件给出一个刻意朴素、独立编写的参考模型：
// 它按操作序列逐步重放，所有生效/撤销/续签判定都在查询时全量扫描，
// 不依赖服务维护的 byClause 索引或 memo，用于随机差分测试。

// NaiveAmendment 朴素模型中的协议。
type NaiveAmendment struct {
	ID           string
	DeclaredEff  int64
	Changes      map[string]int
	Revokes      string
	NeedsCosign  bool
	Signed       map[string]int64 // party -> 签署日
	CosignedDay  int64            // -1 未会签
	CreatedSeq   int
	CompletedDay int64
	CompletedSeq int
}

// NaiveContract 朴素合同快照。
type NaiveContract struct {
	ID         string
	Parties    [2]PartyInput
	Clauses    map[string]int
	Locked     map[string]bool
	StartDay   int64
	ExpiryDay  int64
	Amendments []*NaiveAmendment
	Notices    []naiveNotice
	TermReqs   map[string]int64
	TermDay    int64
	Seq        int
	LastNow    int64
}

type naiveNotice struct {
	party string
	day   int64
}

// NaiveModel 纯重放模型。
type NaiveModel struct {
	contracts map[string]*NaiveContract
	lastNow   int64
	clockSet  bool
}

func NewNaiveModel() *NaiveModel {
	return &NaiveModel{contracts: map[string]*NaiveContract{}}
}

func (m *NaiveModel) checkClock(now int64) error {
	if m.clockSet && now < m.lastNow {
		return errf(ErrClockRollback, "rollback")
	}
	return nil
}

func (m *NaiveModel) advance(now int64) {
	m.lastNow = now
	m.clockSet = true
}

func naiveErrCode(err error) ErrorCode {
	if se, ok := err.(*ServiceError); ok {
		return se.Code
	}
	return 0
}

func (m *NaiveModel) Create(in CreateContractInput) error {
	if err := validateCreate(in); err != nil {
		return err
	}
	if err := m.checkClock(in.Now); err != nil {
		return err
	}
	if m.contracts[in.ID] != nil {
		return errf(ErrIllegalState, "exists")
	}
	c := &NaiveContract{
		ID: in.ID, Parties: in.Parties,
		Clauses: cloneClauses(in.Clauses), Locked: cloneLocked(in.LockedClauses),
		StartDay: in.StartDay, ExpiryDay: in.ExpiryDay,
		TermReqs: map[string]int64{}, TermDay: -1, LastNow: in.Now,
	}
	m.contracts[in.ID] = c
	m.advance(in.Now)
	return nil
}

func (m *NaiveModel) get(id string) *NaiveContract { return m.contracts[id] }

func (m *NaiveModel) Add(in AddAmendmentInput) error {
	if err := validateAdd(in); err != nil {
		return err
	}
	if err := m.checkClock(in.Now); err != nil {
		return err
	}
	c := m.get(in.ContractID)
	if c == nil {
		return errf(ErrNotFound, "no contract")
	}
	if c.TermDay >= 0 {
		return errf(ErrIllegalState, "terminated")
	}
	for _, a := range c.Amendments {
		if a.ID == in.AmendmentID {
			return errf(ErrIllegalState, "dup")
		}
	}
	if in.Revokes != "" {
		found := false
		for _, a := range c.Amendments {
			if a.ID == in.Revokes {
				found = true
			}
		}
		if !found {
			return errf(ErrNotFound, "target")
		}
	}
	needs := false
	for cid := range in.Changes {
		if _, ok := c.Clauses[cid]; !ok {
			return errf(ErrInvalidParam, "unknown clause")
		}
		if c.Locked[cid] {
			needs = true
		}
	}
	c.Seq++
	c.Amendments = append(c.Amendments, &NaiveAmendment{
		ID: in.AmendmentID, DeclaredEff: in.EffectiveDay,
		Changes: cloneClauses(in.Changes), Revokes: in.Revokes,
		NeedsCosign: needs, Signed: map[string]int64{},
		CosignedDay: -1, CompletedDay: -1, CompletedSeq: -1,
		CreatedSeq: c.Seq,
	})
	m.advance(in.Now)
	return nil
}

func (n *NaiveContract) party(id string) (PartyInput, bool) {
	for _, p := range n.Parties {
		if p.ID == id {
			return p, true
		}
	}
	return PartyInput{}, false
}

func (n *NaiveContract) amend(id string) *NaiveAmendment {
	for _, a := range n.Amendments {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (m *NaiveModel) Sign(contractID, amendID, party string, now int64) error {
	if isBlank(contractID) || isBlank(amendID) || isBlank(party) {
		return errf(ErrInvalidParam, "blank")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	c := m.get(contractID)
	if c == nil {
		return errf(ErrNotFound, "no contract")
	}
	a := c.amend(amendID)
	if a == nil {
		return errf(ErrNotFound, "no amendment")
	}
	if c.TermDay >= 0 {
		return errf(ErrIllegalState, "terminated")
	}
	p, ok := c.party(party)
	if !ok {
		return errf(ErrInvalidParam, "bad party")
	}
	if a.CompletedDay >= 0 {
		return errf(ErrIllegalState, "complete")
	}
	if _, dup := a.Signed[party]; dup {
		return errf(ErrIllegalState, "dup sign")
	}
	if now < p.AuthFrom || now > p.AuthUntil {
		return errf(ErrAuthExpired, "auth")
	}
	if len(a.Signed) > 0 {
		var first int64 = now
		for _, d := range a.Signed {
			if d < first {
				first = d
			}
		}
		if now > first+signingDeadlineDays {
			return errf(ErrSigningExpired, "late")
		}
	}
	a.Signed[party] = now
	if len(a.Signed) == 2 {
		a.CompletedDay = now
		c.Seq++
		a.CompletedSeq = c.Seq
	}
	m.advance(now)
	return nil
}

func (m *NaiveModel) Cosign(contractID, amendID string, now int64) error {
	if isBlank(contractID) || isBlank(amendID) {
		return errf(ErrInvalidParam, "blank")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	c := m.get(contractID)
	if c == nil {
		return errf(ErrNotFound, "no contract")
	}
	a := c.amend(amendID)
	if a == nil {
		return errf(ErrNotFound, "no amendment")
	}
	if c.TermDay >= 0 {
		return errf(ErrIllegalState, "terminated")
	}
	if !a.NeedsCosign {
		return errf(ErrIllegalState, "not needed")
	}
	if a.CosignedDay >= 0 {
		return errf(ErrIllegalState, "dup cosign")
	}
	a.CosignedDay = now
	m.advance(now)
	return nil
}

func (m *NaiveModel) Notice(contractID, party string, now int64) error {
	if isBlank(contractID) || isBlank(party) {
		return errf(ErrInvalidParam, "blank")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	c := m.get(contractID)
	if c == nil {
		return errf(ErrNotFound, "no contract")
	}
	if _, ok := c.party(party); !ok {
		return errf(ErrInvalidParam, "bad party")
	}
	if c.TermDay >= 0 {
		return errf(ErrIllegalState, "terminated")
	}
	c.Notices = append(c.Notices, naiveNotice{party, now})
	c.Seq++
	m.advance(now)
	return nil
}

func (m *NaiveModel) EarlyTerm(contractID, party string, now int64) error {
	if isBlank(contractID) || isBlank(party) {
		return errf(ErrInvalidParam, "blank")
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	c := m.get(contractID)
	if c == nil {
		return errf(ErrNotFound, "no contract")
	}
	p, ok := c.party(party)
	if !ok {
		return errf(ErrInvalidParam, "bad party")
	}
	if now < p.AuthFrom || now > p.AuthUntil {
		return errf(ErrAuthExpired, "auth")
	}
	if _, dup := c.TermReqs[party]; dup {
		return errf(ErrIllegalState, "dup term")
	}
	c.TermReqs[party] = now
	c.Seq++
	m.advance(now)
	if len(c.TermReqs) == 2 {
		c.TermDay = now
	}
	return nil
}

// effectiveDay 朴素计算协议生效日；不生效返回 -1。
func (a *NaiveAmendment) effectiveDay(c *NaiveContract) int64 {
	if a.CompletedDay < 0 {
		return -1
	}
	if a.NeedsCosign && a.CosignedDay < 0 {
		return -1
	}
	eff := a.DeclaredEff
	if eff < a.CompletedDay {
		eff = a.CompletedDay
	}
	if a.NeedsCosign && eff < a.CosignedDay {
		eff = a.CosignedDay
	}
	if c.TermDay >= 0 && eff > c.TermDay {
		return -1
	}
	return eff
}

// effectiveRevokerAlive 在 day 日撤销协议 r 是否有效（递归判定再撤销）。
func (c *NaiveContract) revokerAlive(r *NaiveAmendment, day int64) bool {
	eff := r.effectiveDay(c)
	if eff < 0 || day < eff {
		return false
	}
	for _, x := range c.Amendments {
		if x.Revokes == r.ID && c.revokerAlive(x, day) {
			return false
		}
	}
	return true
}

func (c *NaiveContract) alive(a *NaiveAmendment, day int64) bool {
	eff := a.effectiveDay(c)
	if eff < 0 || day < eff {
		return false
	}
	for _, r := range c.Amendments {
		if r.Revokes == a.ID && c.revokerAlive(r, day) {
			return false
		}
	}
	return true
}

// Value 朴素全量扫描某日某条款。
func (c *NaiveContract) Value(clause string, day int64) ValueSource {
	mainVal, mainOK := c.Clauses[clause]
	var best *NaiveAmendment
	for _, a := range c.Amendments {
		v, touches := a.Changes[clause]
		_ = v
		if !touches || !c.alive(a, day) {
			continue
		}
		if best == nil {
			best = a
			continue
		}
		ae, be := a.effectiveDay(c), best.effectiveDay(c)
		if ae > be || (ae == be &&
			(a.CompletedSeq > best.CompletedSeq ||
				(a.CompletedSeq == best.CompletedSeq && a.CreatedSeq > best.CreatedSeq))) {
			best = a
		}
	}
	if best == nil {
		if !mainOK {
			return ValueSource{Kind: "NONE"}
		}
		return ValueSource{Value: mainVal, Kind: "MAIN"}
	}
	return ValueSource{Value: best.Changes[clause], Kind: "AMENDMENT",
		AmendmentID: best.ID, EffectiveDay: best.effectiveDay(c)}
}

func (c *NaiveContract) clauseValAt(clause string, day int64) (int, bool) {
	_, ok := c.Clauses[clause]
	if !ok {
		return 0, false
	}
	return c.Value(clause, day).Value, true
}

// Simulate 朴素续签推演。
func (c *NaiveContract) Simulate(day int64) (expiry int64, inTerm bool, records []RenewalRecord) {
	records = []RenewalRecord{}
	expiry = c.ExpiryDay
	for {
		auto, _ := c.clauseValAt(ClauseAutoRenew, expiry)
		term, _ := c.clauseValAt(ClauseRenewalTerm, expiry)
		notice, _ := c.clauseValAt(ClauseNoticeDays, expiry)
		timely := false
		for _, n := range c.Notices {
			if n.day <= expiry-int64(notice) {
				timely = true
			}
		}
		if c.TermDay >= 0 && c.TermDay <= expiry {
			return expiry, day >= c.StartDay && day <= c.TermDay, records
		}
		if day <= expiry {
			return expiry, day >= c.StartDay && day <= expiry, records
		}
		if auto != 1 || timely || term <= 0 {
			return expiry, false, records
		}
		records = append(records, RenewalRecord{
			FromExpiryDay: expiry, NewExpiryDay: expiry + int64(term), TermLength: term})
		expiry += int64(term)
	}
}

func (m *NaiveModel) Contract(id string) *NaiveContract { return m.contracts[id] }

var _ = naiveErrCode
