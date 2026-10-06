package cedu

import "sync"

// Service 为执业资格继续教育学分周期核算服务。
// 所有方法可并发调用；内部以单一互斥锁串行化，并发调用的结果严格
// 等价于按锁获取顺序的某个串行执行，相同操作序列重放结果一致。
type Service struct {
	mu      sync.Mutex
	cfg     Config
	holders map[string]*holder
	lastNow int // 服务级单调时钟：对不存在持证人的操作也能判定回退
}

// NewService 创建服务并校验全局参数。
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.CycleLength <= 0:
		return nil, newError(ErrInvalidParam, "cycle length must be positive")
	case cfg.TotalRequired <= 0:
		return nil, newError(ErrInvalidParam, "total required credits must be positive")
	case cfg.RequiredMin <= 0:
		return nil, newError(ErrInvalidParam, "required minimum must be positive")
	case cfg.RequiredCap <= 0, cfg.ElectiveCap <= 0:
		return nil, newError(ErrInvalidParam, "category caps must be positive")
	case cfg.GraceDays < 0, cfg.CorrectDays < 0, cfg.CarryoverCap < 0:
		return nil, newError(ErrInvalidParam, "grace/correct/carryover values must be non-negative")
	case cfg.GraceDays > cfg.CycleLength:
		return nil, newError(ErrInvalidParam, "grace days must not exceed cycle length")
	}
	return &Service{cfg: cfg, holders: map[string]*holder{}, lastNow: -1}, nil
}

func (s *Service) clockOK(now int) bool { return now >= s.lastNow }

func (s *Service) boundsOf(h *holder, k int) (start, end int) {
	return cycleBounds(h.issueDate, s.cfg.CycleLength, k)
}

// graceEnd 返回周期 k 的宽限满日：宽限期为 [end, end+GraceDays)。
// end+GraceDays 当天补登取得日为该日的学分已属“晚一日”，不计入。
func (s *Service) graceEnd(h *holder, k int) int {
	_, end := s.boundsOf(h, k)
	return end + s.cfg.GraceDays
}

// winRaw 组装周期 k 的“周期本体”原始量：自有 win 记录；当上周期
// ocPass 时，其宽限带内记录流入本周期本体；上周期结转按选修计入。
func (s *Service) winRaw(h *holder, k int) rawCredit {
	a := h.agg(k)
	r := rawCredit{requiredRaw: a.win[Required], electiveRaw: a.win[Elective], onlineRaw: a.win[Online]}
	if k > 0 {
		if c, ok := h.cache[k-1]; ok && c.outcome == ocPass {
			prev := h.agg(k - 1)
			// 上周期宽限带记录流入本周期时保持原类别；仅结转按选修计入。
			r.electiveRaw += prev.zon[Elective] + c.carryOut
			r.onlineRaw += prev.zon[Online]
		}
	}
	return r
}

// recompute 重算周期 k 的窗口判定与结转（仅读紧邻的 k-1 缓存）。
func (s *Service) recompute(h *holder, k int) cached {
	r := s.winRaw(h, k)
	c := countCredit(r, s.cfg)
	return cached{winMet: c.met(s.cfg), carryOut: carryOut(r, c, s.cfg)}
}

// invalidateFrom 使周期 from 起的级联缓存失效并重算。级联在首个
// 结果未变的周期处停止：单次更正只改一个桶，影响被下游吸收即终止，
// 故总开销不随历史记录总量增长（摊销 O(1)）。
func (s *Service) invalidateFrom(h *holder, from int) {
	for k := from; ; k++ {
		c, ok := h.cache[k]
		if !ok {
			return
		}
		n := s.recompute(h, k)
		n.outcome = c.outcome
		if c.winMet == n.winMet && c.carryOut == n.carryOut {
			return
		}
		h.cache[k] = n
	}
}

// advance 从 h.front 起按 now 终结已到点的周期；可能置 expired。
func (s *Service) advance(h *holder, now int) {
	for !h.expired {
		k := h.front
		_, end := s.boundsOf(h, k)
		ge := s.graceEnd(h, k)
		c := s.recompute(h, k)
		if now < end {
			c.outcome = ocLive
			h.cache[k] = c
			return
		}
		if c.winMet {
			c.outcome = ocPass
			h.cache[k] = c
			h.front = k + 1
			// 连续跨越时新 front 可能持有占位缓存（无 carry 口径），
			// 删除后由本轮 recompute 重建，确保未来周期登记被采纳。
			delete(h.cache, k+1)
			continue
		}
		if now < ge {
			c.outcome = ocLive
			h.cache[k] = c
			return
		}
		a := h.agg(k)
		r := s.winRaw(h, k)
		r.requiredRaw += a.zon[Required]
		r.electiveRaw += a.zon[Elective]
		r.onlineRaw += a.zon[Online]
		if countCredit(r, s.cfg).met(s.cfg) {
			c.outcome = ocGracePass
			c.carryOut = 0
			h.cache[k] = c
			h.front = k + 1
			delete(h.cache, k+1)
			continue
		}
		c.outcome = ocExpired
		h.cache[k] = c
		h.expired = true
		return
	}
}

func nonempty(x string) bool { return x != "" }

// RegisterHolder 注册持证人；已失效者可凭相同 ID 重新注册，旧数据不继承。
func (s *Service) RegisterHolder(in RegisterInput) error {
	if !nonempty(in.HolderID) || in.IssueDate < 0 || in.Now < in.IssueDate {
		return newError(ErrInvalidParam, "invalid holder registration parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clockOK(in.Now) {
		return newError(ErrClockRollback, "now %d < last accepted now %d", in.Now, s.lastNow)
	}
	h := s.holders[in.HolderID]
	if h != nil {
		if !h.expired {
			return newError(ErrStateNotAllowed, "holder %q already registered and active", in.HolderID)
		}
		h = nil
	}
	h = newHolder(in.HolderID, in.IssueDate, in.Now)
	h.events = append(h.events, event{kind: evRegister, at: in.Now, issue: in.IssueDate})
	s.holders[in.HolderID] = h
	s.lastNow = in.Now
	s.advance(h, in.Now)
	return nil
}

func (in CreditInput) valid() bool {
	return nonempty(in.HolderID) && nonempty(in.Org) && in.Category.valid() &&
		in.Credits > 0 && in.EarnedOn >= 0 && in.EarnedOn <= in.Now
}

// RegisterCredit 登记学分，返回记录 ID。
func (s *Service) RegisterCredit(in CreditInput) (string, error) {
	if !in.valid() {
		return "", newError(ErrInvalidParam, "invalid credit registration parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clockOK(in.Now) {
		return "", newError(ErrClockRollback, "now %d < last accepted now %d", in.Now, s.lastNow)
	}
	h := s.holders[in.HolderID]
	if h == nil {
		return "", newError(ErrNotFound, "holder %q not found", in.HolderID)
	}
	if h.expired {
		return "", newError(ErrCertificateExpired, "holder %q certificate expired; re-register first", in.HolderID)
	}
	if s.wouldExpire(h, in.Now) {
		return "", newError(ErrCertificateExpired, "holder %q certificate expired", in.HolderID)
	}
	if in.EarnedOn < h.issueDate {
		return "", newError(ErrInvalidParam, "earned-on %d before issue date %d", in.EarnedOn, h.issueDate)
	}
	// 归属桶必须基于“接受该登记后”的 front：登记时刻可能恰好跨越
	// 周期末日。先用非突变的方式求出推进后的 front（不落地缓存）。
	f := s.frontAt(h, in.Now)
	start, end := s.boundsOf(h, f)
	ge := s.graceEnd(h, f)
	zone := false
	cycle := f
	switch {
	case in.EarnedOn < start:
		return "", newError(ErrStateNotAllowed, "earned-on %d belongs to an already closed cycle", in.EarnedOn)
	case in.EarnedOn < end:
		zone, cycle = false, f
	case in.EarnedOn < ge:
		zone, cycle = true, f
	default:
		zone = false
		cycle = (in.EarnedOn - h.issueDate) / s.cfg.CycleLength
	}
	key := dupKey(in.Org, in.EarnedOn, in.Category)
	if _, dup := h.records[key]; dup {
		return "", newError(ErrDuplicate, "duplicate credit for org/date/category")
	}
	h.seq++
	id := recordID(in.HolderID, h.seq)
	rec := &Record{
		ID: id, HolderID: in.HolderID, Category: in.Category, Credits: in.Credits,
		EarnedOn: in.EarnedOn, Org: in.Org, RegAt: in.Now, cycle: cycle, zone: zone,
	}
	h.records[key] = rec
	h.byID[id] = rec
	h.agg(cycle).add(zone, in.Category, in.Credits)
	h.events = append(h.events, event{
		kind: evCredit, at: in.Now, recordID: id, cat: in.Category,
		credits: in.Credits, earnedOn: in.EarnedOn, org: in.Org,
	})
	s.invalidateFrom(h, min2(cycle, f))
	h.lastNow = in.Now
	s.lastNow = in.Now
	s.advance(h, in.Now)
	return id, nil
}

// wouldExpire 在不修改状态的前提下判断：把时钟推进到 now 后，
// 当前持证人是否必然已失效。依据不变量：时钟推进前 front 周期处于
// “进行中/宽限期”，且不存在取得日晚于上次时钟的记录，故一次推进
// 至多终结一个周期，预判只需考察 front 自身。
func (s *Service) wouldExpire(h *holder, now int) bool {
	k := h.front
	_, end := s.boundsOf(h, k)
	ge := s.graceEnd(h, k)
	c := s.recompute(h, k)
	if now < end || c.winMet || now < ge {
		return false
	}
	a := h.agg(k)
	r := s.winRaw(h, k)
	r.requiredRaw += a.zon[Required]
	r.electiveRaw += a.zon[Elective]
	r.onlineRaw += a.zon[Online]
	return !countCredit(r, s.cfg).met(s.cfg)
}

// frontAt 在不修改任何状态的前提下计算把时钟推进到 now 后的 front。
// 依据与 wouldExpire 相同的不变量：已有记录的取得日均不晚于上次时钟，
// 因此在“尚未加入本条新记录”的状态上，front 只会因 win 达标而顺序推进。
func (s *Service) frontAt(h *holder, now int) int {
	k := h.front
	for {
		_, end := s.boundsOf(h, k)
		if now < end {
			return k
		}
		if !s.recompute(h, k).winMet {
			ge := s.graceEnd(h, k)
			if now < ge {
				return k
			}
			a := h.agg(k)
			r := s.winRaw(h, k)
			r.requiredRaw += a.zon[Required]
			r.electiveRaw += a.zon[Elective]
			r.onlineRaw += a.zon[Online]
			if !countCredit(r, s.cfg).met(s.cfg) {
				return k // 失效：归属不再重要，调用方随后会拒绝
			}
		}
		k++
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type correctArgs struct {
	holderID, recordID, org string
	now                     int
}

// locateCorrect 执行更正/撤销的共享校验，严格按错误优先级返回。
func (s *Service) locateCorrect(h *holder, in correctArgs) (*Record, error) {
	if !s.clockOK(in.now) {
		return nil, newError(ErrClockRollback, "now %d < last accepted now %d", in.now, s.lastNow)
	}
	if h.expired || s.wouldExpire(h, in.now) {
		return nil, newError(ErrCertificateExpired, "holder %q certificate expired", in.holderID)
	}
	rec := h.byID[in.recordID]
	if rec == nil || rec.Org != in.org {
		return nil, newError(ErrNotFound, "record %q for org %q not found", in.recordID, in.org)
	}
	if in.now > rec.RegAt+s.cfg.CorrectDays {
		return nil, newError(ErrCorrectionExpired, "correction window closed at %d", rec.RegAt+s.cfg.CorrectDays)
	}
	return rec, nil
}

// applyMutation 处理更正/撤销后的级联：把 front 回退到受影响周期后
// 重新推进，使结果与记录一开始即为新值完全一致。
func (s *Service) applyMutation(h *holder, affected, now int) {
	if affected < h.front {
		h.front = affected
		for k := range h.cache {
			if k >= affected {
				delete(h.cache, k)
			}
		}
	}
	s.invalidateFrom(h, min2(affected, h.front))
	s.advance(h, now)
}

// CorrectCredit 由出具机构更正学分数；恰在更正期限当天仍允许。
func (s *Service) CorrectCredit(holderID, recordID, org string, credits, now int) error {
	if !nonempty(holderID) || !nonempty(recordID) || !nonempty(org) || now < 0 || credits <= 0 {
		return newError(ErrInvalidParam, "invalid correction parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clockOK(now) {
		return newError(ErrClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	h := s.holders[holderID]
	if h == nil {
		return newError(ErrNotFound, "holder %q not found", holderID)
	}
	rec, err := s.locateCorrect(h, correctArgs{holderID, recordID, org, now})
	if err != nil {
		return err
	}
	old := rec.Credits
	h.agg(rec.cycle).add(rec.zone, rec.Category, credits-old)
	rec.Credits = credits
	h.events = append(h.events, event{
		kind: evCorrect, at: now, recordID: recordID, org: org,
		credits: credits, oldVal: old,
	})
	h.lastNow = now
	s.lastNow = now
	s.applyMutation(h, rec.cycle, now)
	return nil
}

// RevokeCredit 由出具机构撤销记录（撤销后同键可重新登记）。
func (s *Service) RevokeCredit(holderID, recordID, org string, now int) error {
	if !nonempty(holderID) || !nonempty(recordID) || !nonempty(org) || now < 0 {
		return newError(ErrInvalidParam, "invalid revocation parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clockOK(now) {
		return newError(ErrClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	h := s.holders[holderID]
	if h == nil {
		return newError(ErrNotFound, "holder %q not found", holderID)
	}
	rec, err := s.locateCorrect(h, correctArgs{holderID, recordID, org, now})
	if err != nil {
		return err
	}
	h.agg(rec.cycle).add(rec.zone, rec.Category, -rec.Credits)
	rec.Revoked = true
	delete(h.byID, rec.ID)
	delete(h.records, dupKey(rec.Org, rec.EarnedOn, rec.Category))
	h.events = append(h.events, event{kind: evRevoke, at: now, recordID: recordID, org: org})
	h.lastNow = now
	s.lastNow = now
	s.applyMutation(h, rec.cycle, now)
	return nil
}
