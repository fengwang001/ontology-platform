package contacttracing

import "sort"

// System 是住院病区感染病例接触者追踪与隔离期判定系统。
// 所有方法可并发调用，底层用单一互斥锁串行化写操作，结果等价于某个串行顺序。
type System struct {
	store *store
}

// New 创建一个空系统。
func New() *System {
	return &System{store: newStore()}
}

// Stats 是推导工作量仪表，用于验证局部化重推导。
type Stats struct {
	Now          int64
	Cases        int
	Stays        int
	CasesDerived int64
	StaysScanned int64
}

// Stats 返回当前仪表读数（累计值，测试可在操作前后取差）。
func (s *System) Stats() Stats {
	st := s.store
	st.mu.RLock()
	defer st.mu.RUnlock()
	n := 0
	for _, ss := range st.stays {
		n += len(ss)
	}
	return Stats{
		Now:          st.now,
		Cases:        len(st.cases),
		Stays:        n,
		CasesDerived: st.derived.casesDerived,
		StaysScanned: st.derived.staysScanned,
	}
}

// checkClock 推进或校验时钟；调用方须持写锁。
func (st *store) checkClock(now int64) error {
	if now < st.now {
		return errf(ErrClockRollback, "now %d 早于上次接受操作 %d", now, st.now)
	}
	st.now = now
	return nil
}

// invalidateStayChange 使“某患者在某病房的住宿变更”可能影响到的病例缓存失效。
// 仅遍历该患者相关病例与该病房反向索引，不触达全院病例。
func (st *store) invalidateStayChange(patient, room string) {
	seen := map[string]struct{}{}
	for caseID := range st.derived.patientCase[patient] {
		seen[caseID] = struct{}{}
	}
	for caseID := range st.derived.roomCase[room] {
		seen[caseID] = struct{}{}
	}
	for caseID := range seen {
		st.derived.invalidateCase(caseID)
	}
}

func (st *store) registerPatient(p string) {
	st.patients[p] = struct{}{}
}

func (st *store) appendStayLocked(stay *Stay) {
	p := stay.Patient
	st.registerPatient(p)
	st.stays[p] = append(st.stays[p], stay)
	st.roomStays[stay.Room] = append(st.roomStays[stay.Room], stay)
	if stay.CheckOutOpen {
		st.openStay[p] = stay
	}
	st.invalidateStayChange(p, stay.Room)
}

func stayConflicts(existing []*Stay, in, out int64) bool {
	for _, e := range existing {
		eOut := e.CheckOut
		if e.CheckOutOpen {
			// 在住记录右端未定：任何晚于其入住时刻的新区间都与之正重叠。
			eOut = out + 1
		}
		if overlapLen(in, out, e.CheckIn, eOut) > 0 {
			return true
		}
	}
	return false
}

// RecordAdmission 登记入住（出时刻未登记）。
func (s *System) RecordAdmission(patient, room string, now, checkIn int64) error {
	if !nonEmptyID(patient, room) || !validTime(now) || !validTime(checkIn) || checkIn > now {
		return errf(ErrInvalidParameter, "入住参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return err
	}
	if open := st.openStay[patient]; open != nil {
		return errf(ErrInvalidState, "患者 %s 已有未出住记录（病房 %s）", patient, open.Room)
	}
	if stayConflicts(st.stays[patient], checkIn, checkIn+1) {
		return errf(ErrStayConflict, "入住时刻 %d 与既有住宿重叠", checkIn)
	}
	st.appendStayLocked(&Stay{Patient: patient, Room: room, CheckIn: checkIn, CheckOutOpen: true})
	return nil
}

// RecordDischarge 为某患者当前在 room 的在住记录补登出住时刻。
func (s *System) RecordDischarge(patient, room string, now, checkOut int64) error {
	if !nonEmptyID(patient, room) || !validTime(now) || !validTime(checkOut) || checkOut > now {
		return errf(ErrInvalidParameter, "出住参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return err
	}
	open := st.openStay[patient]
	if open == nil {
		return errf(ErrNotFound, "患者 %s 没有未出住记录", patient)
	}
	if open.Room != room {
		return errf(ErrNotFound, "患者 %s 的在住记录不在病房 %s", patient, room)
	}
	if checkOut <= open.CheckIn {
		return errf(ErrInvalidParameter, "出住时刻 %d 须晚于入住时刻 %d", checkOut, open.CheckIn)
	}
	open.CheckOut = checkOut
	open.CheckOutOpen = false
	delete(st.openStay, patient)
	st.invalidateStayChange(patient, room)
	return nil
}

// RecordStay 一次性追补一段已结束的住宿。
func (s *System) RecordStay(patient, room string, now, checkIn, checkOut int64) error {
	if !nonEmptyID(patient, room) || !validTime(now) || !validTime(checkIn) || !validTime(checkOut) ||
		checkIn >= checkOut || checkOut > now {
		return errf(ErrInvalidParameter, "追补住宿参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return err
	}
	if open := st.openStay[patient]; open != nil && checkOut > open.CheckIn {
		return errf(ErrStayConflict, "追补区间与未出住记录重叠")
	}
	if stayConflicts(st.stays[patient], checkIn, checkOut) {
		return errf(ErrStayConflict, "追补区间与既有住宿重叠")
	}
	st.appendStayLocked(&Stay{
		Patient:  patient,
		Room:     room,
		CheckIn:  checkIn,
		CheckOut: checkOut,
	})
	return nil
}

// RegisterCase 登记病例，返回病例标识。
func (s *System) RegisterCase(patient string, now, onsetAt int64) (string, error) {
	if !nonEmptyID(patient) || !validTime(now) || !validTime(onsetAt) || onsetAt > now {
		return "", errf(ErrInvalidParameter, "病例登记参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return "", err
	}
	for _, c := range st.casesByPatient[patient] {
		if !c.Revoked {
			return "", errf(ErrInvalidState, "患者 %s 已存在未撤销病例 %s", patient, c.ID)
		}
	}
	st.lastCase++
	id := caseID(st.lastCase)
	c := &Case{
		ID:            id,
		Patient:       patient,
		OnsetAt:       onsetAt,
		RegisteredAt:  now,
		IsolationOpen: true,
	}
	st.cases[id] = c
	st.casesByPatient[patient] = append(st.casesByPatient[patient], c)
	st.registerPatient(patient)
	// 预索引：病例本人住过的全部病房都可能影响其密接判定，
	// 这样即使该病例从未推导过，按患者查状态也能经由病房反向索引找到候选病例。
	for _, stay := range st.stays[patient] {
		st.derived.indexCase(stay.Room, id)
	}
	st.derived.registerCaseOwnership(patient, id)
	return id, nil
}

func caseID(n int64) string {
	const digits = "0123456789"
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	i--
	buf[i] = 'C'
	return string(buf[i:])
}

// RecordIsolation 为病例登记一次隔离时刻。
func (s *System) RecordIsolation(caseIDValue string, now, isolatedAt int64) error {
	if !nonEmptyID(caseIDValue) || !validTime(now) || !validTime(isolatedAt) || isolatedAt > now {
		return errf(ErrInvalidParameter, "隔离登记参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return err
	}
	c := st.cases[caseIDValue]
	if c == nil {
		return errf(ErrNotFound, "病例 %s 不存在", caseIDValue)
	}
	if c.Revoked {
		return errf(ErrInvalidState, "病例 %s 已撤销", caseIDValue)
	}
	if !c.IsolationOpen {
		return errf(ErrInvalidState, "病例 %s 已登记隔离", caseIDValue)
	}
	start := c.OnsetAt - InfectiousLeadMinutes
	if start < 0 {
		start = 0
	}
	if isolatedAt < start {
		return errf(ErrInvalidParameter, "隔离时刻 %d 早于传染期起点 %d", isolatedAt, start)
	}
	c.IsolatedAt = isolatedAt
	c.IsolationOpen = false
	st.derived.invalidateCase(c.ID)
	return nil
}

// CorrectOnset 改正病例的发病时刻，传染期随之改变。
func (s *System) CorrectOnset(caseIDValue string, now, onsetAt int64) error {
	if !nonEmptyID(caseIDValue) || !validTime(now) || !validTime(onsetAt) || onsetAt > now {
		return errf(ErrInvalidParameter, "改正发病时刻参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return err
	}
	c := st.cases[caseIDValue]
	if c == nil {
		return errf(ErrNotFound, "病例 %s 不存在", caseIDValue)
	}
	if c.Revoked {
		return errf(ErrInvalidState, "病例 %s 已撤销", caseIDValue)
	}
	c.OnsetAt = onsetAt
	st.derived.invalidateCase(c.ID)
	return nil
}

// RevokeCase 撤销病例；撤销后它不再产生任何接触者。
func (s *System) RevokeCase(caseIDValue string, now int64) error {
	if !nonEmptyID(caseIDValue) || !validTime(now) {
		return errf(ErrInvalidParameter, "撤销病例参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return err
	}
	c := st.cases[caseIDValue]
	if c == nil {
		return errf(ErrNotFound, "病例 %s 不存在", caseIDValue)
	}
	if c.Revoked {
		return errf(ErrInvalidState, "病例 %s 已撤销", caseIDValue)
	}
	c.Revoked = true
	st.derived.invalidateCase(c.ID)
	return nil
}

// ContactEntry 是清单中的一个接触者条目。
type ContactEntry struct {
	Patient       string
	Kind          ContactKind
	LastContactAt int64
}

// ListContacts 返回某病例当前全部密接与次密接。
func (s *System) ListContacts(caseIDValue string, now int64) ([]ContactEntry, error) {
	if !nonEmptyID(caseIDValue) || !validTime(now) {
		return nil, errf(ErrInvalidParameter, "清单查询参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return nil, err
	}
	c := st.cases[caseIDValue]
	if c == nil {
		return nil, errf(ErrNotFound, "病例 %s 不存在", caseIDValue)
	}
	if c.Revoked {
		return []ContactEntry{}, nil
	}
	res := st.deriveCase(c.ID, now)
	entries := make([]ContactEntry, 0, len(res.sources))
	for p, info := range res.sources {
		entries = append(entries, ContactEntry{Patient: p, Kind: info.kind, LastContactAt: info.lastContactAt})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Patient < entries[j].Patient
	})
	return entries, nil
}

// StatusResult 是患者状态查询结果。
type StatusResult struct {
	Status    PatientStatus
	ReleaseAt int64 // Status 非 UNRELATED 时有效
}

func releaseBoundary(kind ContactKind, lastContactAt int64) int64 {
	if kind == KindClose {
		return lastContactAt + CloseQuarantineMinutes
	}
	return lastContactAt + SecondaryObservationMinutes
}

// PatientStatusAt 查询患者在 now 的状态。
// 只读取该患者作为病例本人/接触者参与过的病例，不扫全院。
func (s *System) PatientStatusAt(patient string, now int64) (StatusResult, error) {
	if !nonEmptyID(patient) || !validTime(now) {
		return StatusResult{}, errf(ErrInvalidParameter, "状态查询参数非法")
	}
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkClock(now); err != nil {
		return StatusResult{}, err
	}
	if _, ok := st.patients[patient]; !ok {
		return StatusResult{}, errf(ErrNotFound, "患者 %s 不存在", patient)
	}

	activeKindSet := false
	var activeKind ContactKind
	var activeRelease, everRelease int64
	ever := false

	// 候选集可能因“次密接尚未推导、其病房索引未建立”而缺病例，
	// 故先推导一轮候选以建立索引，再收集一次候选（定点一次即可：
	// 次密接只依赖密接，密接病房在病例本人与密接推导后已全部建立）。
	firstRound := st.candidateCases(patient)
	for caseID := range firstRound {
		if c := st.cases[caseID]; c != nil && !c.Revoked {
			st.deriveCase(caseID, now)
		}
	}
	for caseID := range st.candidateCases(patient) {
		c := st.cases[caseID]
		if c == nil || c.Revoked || c.Patient == patient {
			continue
		}
		info, ok := st.deriveCase(caseID, now).sources[patient]
		if !ok {
			continue
		}
		boundary := releaseBoundary(info.kind, info.lastContactAt)
		ever = true
		if boundary > everRelease {
			everRelease = boundary
		}
		if now < boundary {
			if !activeKindSet || info.kind == KindClose {
				activeKind = info.kind
				activeKindSet = true
			}
			if boundary > activeRelease {
				activeRelease = boundary
			}
		}
	}

	if !ever {
		return StatusResult{Status: StatusUnrelated}, nil
	}
	if activeKindSet {
		status := StatusSecondaryObservation
		if activeKind == KindClose {
			status = StatusCloseQuarantine
		}
		return StatusResult{Status: status, ReleaseAt: activeRelease}, nil
	}
	return StatusResult{Status: StatusReleased, ReleaseAt: everRelease}, nil
}
