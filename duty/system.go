package duty

import "sync"

// System 是合规系统。并发安全：所有操作在同一互斥锁下串行化，
// 因而天然等价于某个串行顺序，重放同一操作序列结论一致。
type System struct {
	mu     sync.Mutex
	cfg    Config
	clock  int
	nextID int
	people map[int]*person
	dIndex map[int]*Duty // 全局值勤期 ID -> 值勤期（O(1) 定位，不随历史增长）
}

type person struct {
	id     int
	quals  map[int]int // 机型 -> 到期时刻；不存在即未持有/已吊销
	duties []*Duty     // 按 Start 升序，互不重叠
}

// NewSystem 创建系统。配置须满足字段均为非负值、边界满足 0<b1<b2<=DayLen。
func NewSystem(cfg Config) *System {
	return &System{cfg: cfg, nextID: 1, people: map[int]*person{}, dIndex: map[int]*Duty{}}
}

func (s *System) rollback(now int) bool { return now < s.clock }

// findPos 返回首个 Start > start 的下标，即插入位置；其前后为相邻值勤期。
func findPos(ds []*Duty, start int) int {
	lo, hi := 0, len(ds)
	for lo < hi {
		mid := (lo + hi) / 2
		if ds[mid].Start < start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func findDuty(ds []*Duty, id int) int {
	for i, d := range ds {
		if d.ID == id {
			return i
		}
	}
	return -1
}

// selectWindow 返回与窗 [w0, w0+win) 重叠的值勤期，供滚动判定使用。
func selectWindow(ds []*Duty, w0, win int) []*Duty {
	i := findPos(ds, w0) // 第一个 Start >= w0
	if i > 0 {
		i-- // 可能从左侧跨入的一个
	}
	var out []*Duty
	for j := i; j < len(ds); j++ {
		if ds[j].Start >= w0+win {
			break
		}
		if ds[j].End > w0 {
			out = append(out, ds[j])
		}
	}
	return out
}

// checkRegister 在不修改状态的前提下，对候选值勤期执行 6..12 类检查。
// pos 为插入位置（调用方需保证 start 不与任何既有值勤期同起点）。
func (s *System) checkRegister(p *person, pos int, ns, ne, legs, aircraft int) Result {
	expiry, held := p.quals[aircraft]
	if !QualValid(expiry, ne, held) {
		return Result{Reject: RejectQualification}
	}

	ds := p.duties
	// 休息：与前一个、与后一个的间隔同时检查；恰等于较大者视为足够。
	length := ne - ns
	if pos > 0 {
		prev := ds[pos-1]
		if prev.End >= ns {
			return Result{Reject: RejectOverlap} // 含端点相接
		}
		if ns-prev.End < s.cfg.RestRequired(prev.Length()) {
			return Result{Reject: RejectRest}
		}
	}
	if pos < len(ds) {
		nxt := ds[pos]
		if ne >= nxt.Start {
			return Result{Reject: RejectOverlap} // 含端点相接
		}
		if nxt.Start-ne < s.cfg.RestRequired(length) {
			return Result{Reject: RejectRest}
		}
	}

	// 单次值勤上限。
	if length > s.cfg.SingleLimit(ns, legs) {
		return Result{Reject: RejectSingleLimit}
	}

	// 7 日累计、28 日累计。与任一相关窗有交的值勤期才纳入：
	// 窗起点范围 [ns-win, ne)，其窗并集为 [ns-win, ne+win)。
	sub := selectWindow(ds, ns-s.cfg.Window7, length+2*s.cfg.Window7)
	if w := rollingViolation(sub, ns, ne, s.cfg.Window7, s.cfg.Limit7); w >= 0 {
		return Result{Reject: RejectRolling7, WindowStart: w}
	}
	sub28 := selectWindow(ds, ns-s.cfg.Window28, length+2*s.cfg.Window28)
	if w := rollingViolation(sub28, ns, ne, s.cfg.Window28, s.cfg.Limit28); w >= 0 {
		return Result{Reject: RejectRolling28, WindowStart: w}
	}
	return Result{}
}

// checkExtend 对“把 pos 处值勤期延长到 newEnd”执行 6..12 类检查。
func (s *System) checkExtend(p *person, pos, newEnd int) Result {
	d := p.duties[pos]
	expiry, held := p.quals[d.Aircraft]
	if !QualValid(expiry, newEnd, held) {
		return Result{Reject: RejectQualification}
	}

	ds := p.duties
	nxt := pos + 1
	if nxt < len(ds) && newEnd > ds[nxt].Start {
		return Result{Reject: RejectOverlap}
	}

	newLen := newEnd - d.Start
	if nxt < len(ds) {
		if ds[nxt].Start-newEnd < s.cfg.RestRequired(newLen) {
			return Result{Reject: RejectRest}
		}
	}

	if newLen > s.cfg.SingleLimit(d.Start, d.Legs)+s.cfg.MaxExtension {
		return Result{Reject: RejectSingleLimit}
	}

	// 延长规则（第 10 类，排在单次超限之后）：至多延长一次、延长量不超过
	// MaxExtension；任意 Window7 窗内至多一个已延长值勤期。
	if d.Extended || newEnd-d.OrigEnd > s.cfg.MaxExtension {
		return Result{Reject: RejectExtension}
	}
	// 存在整数窗 [w,w+W7) 与两个区间都严格相交，当且仅当
	// min(ends)-max(starts) < W7。
	for _, e := range ds {
		if e == d || !e.Extended {
			continue
		}
		latestStart := d.Start
		if e.Start > latestStart {
			latestStart = e.Start
		}
		earliestEnd := newEnd
		if e.End < earliestEnd {
			earliestEnd = e.End
		}
		if earliestEnd-latestStart < s.cfg.Window7 {
			return Result{Reject: RejectExtension}
		}
	}

	// 累计按延长后时长重新成立。临时替换本值勤期终点进行判定。
	oldEnd := d.End
	d.End = newEnd
	span7 := selectWindow(ds, d.Start-s.cfg.Window7, (newEnd-d.Start)+2*s.cfg.Window7)
	sub7 := excludeDuty(span7, d)
	w7 := rollingViolation(sub7, d.Start, newEnd, s.cfg.Window7, s.cfg.Limit7)
	var w28 int
	if w7 < 0 {
		span28 := selectWindow(ds, d.Start-s.cfg.Window28, (newEnd-d.Start)+2*s.cfg.Window28)
		sub28 := excludeDuty(span28, d)
		w28 = rollingViolation(sub28, d.Start, newEnd, s.cfg.Window28, s.cfg.Limit28)
	}
	d.End = oldEnd
	if w7 >= 0 {
		return Result{Reject: RejectRolling7, WindowStart: w7}
	}
	if w28 >= 0 {
		return Result{Reject: RejectRolling28, WindowStart: w28}
	}
	return Result{}
}

func excludeDuty(ds []*Duty, skip *Duty) []*Duty {
	out := make([]*Duty, 0, len(ds))
	for _, d := range ds {
		if d != skip {
			out = append(out, d)
		}
	}
	return out
}

// AddPerson 登记人员。重复 ID 视为参数非法。
func (s *System) AddPerson(now, personID int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || personID < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if s.rollback(now) {
		return Result{Reject: RejectClockRollback}
	}
	if _, exists := s.people[personID]; exists {
		return Result{Reject: RejectInvalidParams}
	}
	s.people[personID] = &person{id: personID, quals: map[int]int{}}
	s.clock = now
	return Result{}
}

// UpdateQualification 更新（或新增）人员的机型资质到期时刻。
func (s *System) UpdateQualification(now, personID, aircraft, expiry int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || personID < 0 || aircraft < 0 || expiry < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if s.rollback(now) {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := s.people[personID]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	p.quals[aircraft] = expiry
	s.clock = now
	return Result{}
}

// RevokeQualification 吊销机型资质；不影响已登记值勤期。
func (s *System) RevokeQualification(now, personID, aircraft int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || personID < 0 || aircraft < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if s.rollback(now) {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := s.people[personID]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	delete(p.quals, aircraft)
	s.clock = now
	return Result{}
}

// Register 登记新值勤期，返回结论与（接受时）新值勤期 ID。
func (s *System) Register(now, personID, start, end, legs, aircraft int) (Result, Accepted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || personID < 0 || start < 0 || end <= start || legs < 0 || legs > 8 || aircraft < 0 {
		return Result{Reject: RejectInvalidParams}, Accepted{}
	}
	if s.rollback(now) {
		return Result{Reject: RejectClockRollback}, Accepted{}
	}
	p, ok := s.people[personID]
	if !ok {
		return Result{Reject: RejectNoPerson}, Accepted{}
	}
	pos := findPos(p.duties, start)
	r := s.checkRegister(p, pos, start, end, legs, aircraft)
	if !r.OK() {
		return r, Accepted{}
	}
	d := &Duty{
		ID: s.nextID, PersonID: personID, Start: start, End: end,
		Legs: legs, Aircraft: aircraft, OrigEnd: end,
	}
	s.nextID++
	p.duties = append(p.duties, nil)
	copy(p.duties[pos+1:], p.duties[pos:])
	p.duties[pos] = d
	s.dIndex[d.ID] = d
	s.clock = now
	return Result{}, Accepted{DutyID: d.ID}
}

// Extend 延长已存在的值勤期至 newEnd。
func (s *System) Extend(now, personID, dutyID, newEnd int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || personID < 0 || dutyID < 0 || newEnd < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if s.rollback(now) {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := s.people[personID]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	d, exists := s.dIndex[dutyID]
	if !exists || d.PersonID != personID {
		return Result{Reject: RejectNoDuty}
	}
	if d.End <= now { // 已解除不可延长；已开始但未解除仍可延长
		return Result{Reject: RejectImmutable}
	}
	if newEnd <= d.End {
		return Result{Reject: RejectInvalidParams}
	}
	pos := findDuty(p.duties, dutyID)
	r := s.checkExtend(p, pos, newEnd)
	if !r.OK() {
		return r
	}
	d.End = newEnd
	d.Extended = true
	s.clock = now
	return Result{}
}

// Cancel 撤销值勤期。
func (s *System) Cancel(now, personID, dutyID int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || personID < 0 || dutyID < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if s.rollback(now) {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := s.people[personID]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	d, exists := s.dIndex[dutyID]
	if !exists || d.PersonID != personID {
		return Result{Reject: RejectNoDuty}
	}
	if d.Start <= now {
		return Result{Reject: RejectImmutable}
	}
	pos := findDuty(p.duties, dutyID)
	p.duties = append(p.duties[:pos], p.duties[pos+1:]...)
	delete(s.dIndex, dutyID)
	s.clock = now
	return Result{}
}

// Duties 返回人员当前值勤期快照（按报到时刻升序）。
func (s *System) Duties(personID int) []Duty {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.people[personID]
	if !ok {
		return nil
	}
	out := make([]Duty, len(p.duties))
	for i, d := range p.duties {
		out[i] = *d
	}
	return out
}
