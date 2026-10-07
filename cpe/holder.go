package cpe

// record 为一条学分登记记录。
type record struct {
	id            string
	category      Category
	credits       int
	earnedDate    int
	org           string
	registeredNow int
	revoked       bool
	cycle         int // 归属周期序号，-1 表示不计入任何周期
}

// cycleState 为单个周期的内部状态。关闭后的周期不可变。
type cycleState struct {
	index         int
	start         int
	end           int
	phase         CyclePhase // Open / Grace；关闭后为 Closed
	closed        bool
	passed        bool
	passedInGrace bool
	graceEntered  bool
	carryIn       int
	tally         Tally // 增量维护；Elective 含 carryIn
	counted       Counted
	carryOut      int
}

func newCycle(index, start int, cfg Config, carryIn int) *cycleState {
	c := &cycleState{
		index:   index,
		start:   start,
		end:     start + cfg.CycleLengthDays,
		phase:   PhaseOpen,
		carryIn: carryIn,
	}
	c.tally.Elective = carryIn // 结转按选修类计入
	return c
}

// snapshot 为某次被接受操作之后的不可变状态快照（O(1) 生成）。
type snapshot struct {
	now        int
	status     CertStatus
	generation int
	issueDate  int
	closed     []*cycleState // 仅追加且元素不可变，切片头按值复制即安全
	cur        cycleState    // 值拷贝；status 为 Active 时有效
	hasCur     bool
}

// holder 为持证人的全部可变状态。
type holder struct {
	id         string
	issueDate  int
	generation int
	status     CertStatus
	closed     []*cycleState // 已关闭周期（追加-only，元素不可变）
	cur        *cycleState   // 当前周期；证书失效时为 nil
	records    map[string]*record
	dedup      map[string]string // (org|earnedDate|category) -> recordID，仅未撤销记录
	snaps      []snapshot        // 跨换发保留，now 单调不减
}

func newHolder(id string, issueDate int, cfg Config) *holder {
	h := &holder{
		id:      id,
		status:  CertActive,
		records: map[string]*record{},
		dedup:   map[string]string{},
		snaps:   []snapshot{},
	}
	h.resetLife(issueDate, cfg)
	return h
}

// resetLife 以新发证日开启一代证书生命（原学分不继承）。
func (h *holder) resetLife(issueDate int, cfg Config) {
	h.issueDate = issueDate
	h.status = CertActive
	h.closed = nil
	h.cur = newCycle(0, issueDate, cfg, 0)
	h.records = map[string]*record{}
	h.dedup = map[string]string{}
}

// dedupKey 重复登记判定键：同一机构、同一取得日、同一类别。
func dedupKey(org string, earnedDate int, cat Category) string {
	return org + "|" + itoa(earnedDate) + "|" + itoa(int(cat))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// advanceTo 将状态机推进到 now（仅闭合已到期的周期）。幂等、确定性。
func (h *holder) advanceTo(now int, cfg Config) {
	for h.status == CertActive {
		c := h.cur
		if c.phase == PhaseOpen {
			if now < c.end {
				return
			}
			counted := countCredits(c.tally, cfg)
			if _, _, ok := evaluate(counted, cfg); ok {
				h.closeCycle(counted, carryover(counted, cfg), false, cfg)
			} else if cfg.GraceDays > 0 {
				c.phase = PhaseGrace
				c.graceEntered = true
			} else {
				h.expire(counted)
			}
		} else { // PhaseGrace
			if now < c.end+cfg.GraceDays {
				return
			}
			counted := countCredits(c.tally, cfg)
			if _, _, ok := evaluate(counted, cfg); ok {
				h.closeCycle(counted, 0, true, cfg) // 宽限期内补足达标：无结转
			} else {
				h.expire(counted)
			}
		}
	}
}

// closeCycle 关闭当前周期（达标），开启下一周期。
func (h *holder) closeCycle(counted Counted, carryOut int, inGrace bool, cfg Config) {
	c := h.cur
	c.phase = PhaseClosed
	c.closed = true
	c.passed = true
	c.passedInGrace = inGrace
	c.counted = counted
	c.carryOut = carryOut
	h.closed = append(h.closed, c)
	h.cur = newCycle(c.index+1, c.end, cfg, carryOut)
}

// expire 关闭当前周期（未达标）并使证书失效。
func (h *holder) expire(counted Counted) {
	c := h.cur
	c.phase = PhaseClosed
	c.closed = true
	c.passed = false
	c.counted = counted
	c.carryOut = 0
	h.closed = append(h.closed, c)
	h.cur = nil
	h.status = CertExpired
}

// refreshGracePass 宽限期内每次操作后检查是否补足达标。
func (h *holder) refreshGracePass(cfg Config) {
	if h.status != CertActive || h.cur == nil || h.cur.phase != PhaseGrace {
		return
	}
	counted := countCredits(h.cur.tally, cfg)
	if _, _, ok := evaluate(counted, cfg); ok {
		h.closeCycle(counted, 0, true, cfg)
	}
}

// attribute 决定新登记记录计入哪个周期（-1 表示不计入）。
// 调用前须已 advanceTo(now)。
func (h *holder) attribute(earnedDate, now int, cfg Config) int {
	c := h.cur
	if c == nil {
		return -1
	}
	if c.phase == PhaseGrace {
		// 宽限期内：取得日落在本周期区间或宽限窗口内均计入本周期。
		if earnedDate >= c.start && earnedDate < c.end+cfg.GraceDays {
			return c.index
		}
		return -1
	}
	// Open：取得日须落在本周期区间。
	if earnedDate < c.start || earnedDate >= c.end {
		return -1
	}
	// 上一周期曾进入宽限期：取得日属于其宽限窗口、且登记时刻已过宽限结束时刻的，一律不计入。
	if len(h.closed) > 0 {
		prev := h.closed[len(h.closed)-1]
		if prev.graceEntered && earnedDate >= prev.end &&
			earnedDate < prev.end+cfg.GraceDays && now >= prev.end+cfg.GraceDays {
			return -1
		}
	}
	return c.index
}

// cycleByIndex 返回第 idx 个周期（已关闭或当前）。
func (h *holder) cycleByIndex(idx int) *cycleState {
	if h.cur != nil && h.cur.index == idx {
		return h.cur
	}
	if idx >= 0 && idx < len(h.closed) {
		return h.closed[idx]
	}
	return nil
}

// snapshot 生成当前状态快照。
func (h *holder) snapshot(now int) {
	s := snapshot{
		now:        now,
		status:     h.status,
		generation: h.generation,
		issueDate:  h.issueDate,
		closed:     h.closed,
	}
	if h.cur != nil {
		s.cur = *h.cur
		s.hasCur = true
	}
	h.snaps = append(h.snaps, s)
}

// viewAt 由快照派生 asOf 时刻的视图（只读，不改原状态）。
func (s *snapshot) viewAt(h *holder, asOf int, cfg Config) View {
	// 派生副本：closed 用全量切片表达式限制容量，append 时强制重新分配，避免共享底层数组。
	derived := &holder{
		id:         h.id,
		issueDate:  s.issueDate,
		generation: s.generation,
		status:     s.status,
		closed:     s.closed[:len(s.closed):len(s.closed)],
	}
	if s.hasCur {
		curCopy := s.cur
		derived.cur = &curCopy
	}
	derived.advanceTo(asOf, cfg)
	return derived.buildView(asOf, cfg)
}

// buildView 构造当前状态的视图（调用前状态须已推进到 asOf）。
func (h *holder) buildView(asOf int, cfg Config) View {
	v := View{
		HolderID:   h.id,
		Generation: h.generation,
		AsOf:       asOf,
		Cert:       h.status,
	}
	for _, c := range h.closed {
		v.Cycles = append(v.Cycles, cycleViewOf(c, cfg))
	}
	if h.cur != nil {
		v.Cycles = append(v.Cycles, cycleViewOf(h.cur, cfg))
	}
	return v
}

func cycleViewOf(c *cycleState, cfg Config) CycleView {
	cv := CycleView{
		Index:         c.index,
		Start:         c.start,
		End:           c.end,
		Phase:         c.phase,
		Passed:        c.passed,
		PassedInGrace: c.passedInGrace,
		GraceEntered:  c.graceEntered,
		CarryIn:       c.carryIn,
		Raw:           c.tally,
		GraceStart:    c.end,
		GraceEnd:      c.end + cfg.GraceDays,
	}
	if c.closed {
		cv.Counted = c.counted
		cv.CarryOut = c.carryOut
	} else {
		cv.Counted = countCredits(c.tally, cfg)
		cv.CarryOut = carryover(cv.Counted, cfg)
	}
	cv.MeetsTotal, cv.MeetsMandatory, cv.Pass = evaluate(cv.Counted, cfg)
	return cv
}

// derive 生成用于只读检查的轻量副本（仅当前周期深拷贝）。
func (h *holder) derive() *holder {
	d := &holder{
		id:         h.id,
		issueDate:  h.issueDate,
		generation: h.generation,
		status:     h.status,
		closed:     h.closed[:len(h.closed):len(h.closed)],
	}
	if h.cur != nil {
		curCopy := *h.cur
		d.cur = &curCopy
	}
	return d
}
