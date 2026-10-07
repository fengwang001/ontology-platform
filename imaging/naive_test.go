package imaging

// naive_test.go 是被测系统的独立朴素参照实现：
// 全部状态用切片保存，判定用线性扫描，不共享 System 的任何内部逻辑。
// 它与 System 暴露相同的方法签名，供随机对照测试逐操作比对。

import "fmt"

type naiveInterval struct {
	s, e int
	id   string
}

type naiveDevice struct {
	id       string
	category DeviceCategory
	field    int
	qc       [][2]int
	occ      []naiveInterval
}

type naiveExamType struct {
	id       string
	category DeviceCategory
	occupy   int
	enhanced bool
	clean    int
}

type naiveRenal struct {
	value      int
	sampleTime int
	seq        int64
}

type naivePatient struct {
	id         string
	highRisk   bool
	hasImplant bool
	maxField   int
	allergy    bool
	renals     []naiveRenal
}

type naiveBooking struct {
	id         string
	patientID  string
	examTypeID string
	deviceID   string
	start      int
	state      BookingState
	held       bool
	hydration  *int
	premed     *int
}

type naiveSystem struct {
	cfg       Config
	now       int
	started   bool
	seq       int64
	devices   map[string]*naiveDevice
	examTypes map[string]*naiveExamType
	patients  map[string]*naivePatient
	bookings  map[string]*naiveBooking
	obs       []naiveInterval // 当前持有的全部留观区间
}

func newNaive(cfg Config) *naiveSystem {
	return &naiveSystem{
		cfg:       cfg,
		devices:   map[string]*naiveDevice{},
		examTypes: map[string]*naiveExamType{},
		patients:  map[string]*naivePatient{},
		bookings:  map[string]*naiveBooking{},
	}
}

func (n *naiveSystem) checkClock(now int) *Error {
	if n.started && now < n.now {
		return errf(CodeClockRollback, fmt.Sprintf("now=%d 小于上次被接受操作的 now=%d", now, n.now))
	}
	return nil
}

func (n *naiveSystem) advance(now int) {
	n.now = now
	n.started = true
}

func (n *naiveSystem) RegisterDevice(now int, id string, category DeviceCategory, field int) *Error {
	if !validID(id) || !validTime(now) || (category != CT && category != MR) {
		return errf(CodeInvalidParam, "设备标识为空、时间越界或类别非法")
	}
	if category == MR && !positive(field) {
		return errf(CodeInvalidParam, "磁共振设备场强须为正整数")
	}
	if category == CT && field != 0 {
		return errf(CodeInvalidParam, "CT 设备不登记场强")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	if _, dup := n.devices[id]; dup {
		return errf(CodeStateMismatch, "设备标识已存在: "+id)
	}
	n.devices[id] = &naiveDevice{id: id, category: category, field: field}
	n.advance(now)
	return nil
}

func (n *naiveSystem) RegisterQC(now int, deviceID string, startMin, endMin int) *Error {
	if !validID(deviceID) || !validTime(now) || startMin < 0 || startMin >= endMin || endMin > DayMinutes {
		return errf(CodeInvalidParam, "质控时段须满足 0<=start<end<=1440")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	d, ok := n.devices[deviceID]
	if !ok {
		return errf(CodeNotFound, "设备不存在: "+deviceID)
	}
	d.qc = append(d.qc, [2]int{startMin, endMin})
	n.advance(now)
	return nil
}

func (n *naiveSystem) RegisterExamType(now int, id string, category DeviceCategory, occupy int, enhanced bool, clean int) *Error {
	if !validID(id) || !validTime(now) || (category != CT && category != MR) ||
		!positive(occupy) || !positive(clean) {
		return errf(CodeInvalidParam, "检查类型参数非法（标识/类别/时长）")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	if _, dup := n.examTypes[id]; dup {
		return errf(CodeStateMismatch, "检查类型标识已存在: "+id)
	}
	n.examTypes[id] = &naiveExamType{id: id, category: category, occupy: occupy, enhanced: enhanced, clean: clean}
	n.advance(now)
	return nil
}

func (n *naiveSystem) RegisterPatient(now int, id string, highRisk, hasImplant bool, maxField int, contrastAllergy bool) *Error {
	if !validID(id) || !validTime(now) {
		return errf(CodeInvalidParam, "患者标识为空或时间越界")
	}
	if hasImplant && !positive(maxField) {
		return errf(CodeInvalidParam, "有植入物时允许的最大场强须为正整数")
	}
	if !hasImplant && maxField != 0 {
		return errf(CodeInvalidParam, "无植入物时最大场强须为 0")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	if _, dup := n.patients[id]; dup {
		return errf(CodeStateMismatch, "患者标识已存在: "+id)
	}
	n.patients[id] = &naivePatient{id: id, highRisk: highRisk, hasImplant: hasImplant, maxField: maxField, allergy: contrastAllergy}
	n.advance(now)
	return nil
}

func (n *naiveSystem) RegisterRenal(now int, patientID string, value, sampleTime int) *Error {
	if !validID(patientID) || !validTime(now) || !validTime(sampleTime) {
		return errf(CodeInvalidParam, "患者标识为空或时间越界")
	}
	if sampleTime > now {
		return errf(CodeInvalidParam, "采样时刻不得晚于 now")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	p, ok := n.patients[patientID]
	if !ok {
		return errf(CodeNotFound, "患者不存在: "+patientID)
	}
	n.seq++
	p.renals = append(p.renals, naiveRenal{value: value, sampleTime: sampleTime, seq: n.seq})
	n.advance(now)
	return nil
}

// latestRenal 线性扫描取采样时刻最晚者，并列取登记序号最大者。
func (p *naivePatient) latestRenal() *naiveRenal {
	var best *naiveRenal
	for i := range p.renals {
		r := &p.renals[i]
		if best == nil || r.sampleTime > best.sampleTime ||
			(r.sampleTime == best.sampleTime && r.seq > best.seq) {
			best = r
		}
	}
	return best
}

func (n *naiveSystem) evalRenal(p *naivePatient, start int) (Code, string, bool) {
	r := p.latestRenal()
	if r == nil {
		return CodeRenalMissingOrExpired, "无肾功能结果", false
	}
	if r.sampleTime > start {
		return CodeRenalMissingOrExpired,
			fmt.Sprintf("最新结果采样时刻 %d 晚于判定时刻 %d", r.sampleTime, start), false
	}
	validity := n.cfg.RenalValidNormal
	if p.highRisk {
		validity = n.cfg.RenalValidHighRisk
	}
	if start-r.sampleTime > validity {
		return CodeRenalMissingOrExpired,
			fmt.Sprintf("结果已超期：%d-%d=%d > 有效期 %d", start, r.sampleTime, start-r.sampleTime, validity), false
	}
	if r.value < n.cfg.RenalLower {
		return CodeRenalInsufficient,
			fmt.Sprintf("结果数值 %d 低于下限 %d", r.value, n.cfg.RenalLower), false
	}
	return OK, "", r.value < n.cfg.RenalUpper
}

func naiveQCConflict(d *naiveDevice, s, e int) bool {
	for day := s / DayMinutes; day <= (e-1)/DayMinutes; day++ {
		base := day * DayMinutes
		for _, w := range d.qc {
			if base+w[0] < e && s < base+w[1] {
				return true
			}
		}
	}
	return false
}

// naiveObsMax 线性扫描计算 [s,e) 内最大留观人数。
func (n *naiveSystem) naiveObsMax(s, e int) int {
	points := []int{s}
	for _, iv := range n.obs {
		if iv.s > s && iv.s < e {
			points = append(points, iv.s)
		}
	}
	best := 0
	for _, t := range points {
		cnt := 0
		for _, iv := range n.obs {
			if iv.s <= t && t < iv.e {
				cnt++
			}
		}
		if cnt > best {
			best = cnt
		}
	}
	return best
}

func (n *naiveSystem) validateSlot(p *naivePatient, et *naiveExamType, d *naiveDevice, start int) *Error {
	if et.category != d.category {
		return errf(CodeDeviceCategoryMismatch,
			fmt.Sprintf("检查类型 %s 要求 %s，设备 %s 为 %s", et.id, et.category, d.id, d.category))
	}
	if d.category == MR && p.hasImplant && d.field > p.maxField {
		return errf(CodeImplantIncompatible,
			fmt.Sprintf("设备场强 %d 超过患者允许的最大场强 %d", d.field, p.maxField))
	}
	if et.enhanced {
		if code, detail, _ := n.evalRenal(p, start); code != OK {
			return errf(code, detail)
		}
	}
	end := start + et.occupy + et.clean
	for _, iv := range d.occ {
		if iv.s < end && start < iv.e {
			return errf(CodeDeviceConflict, fmt.Sprintf("设备 %s 在 [%d,%d) 已有占用", d.id, start, end))
		}
	}
	if naiveQCConflict(d, start, end) {
		return errf(CodeQCConflict,
			fmt.Sprintf("设备 %s 的占用区间 [%d,%d) 与每日质控时段重叠", d.id, start, end))
	}
	if et.enhanced && n.cfg.ObservationDuration > 0 {
		os, oe := start+et.occupy, start+et.occupy+n.cfg.ObservationDuration
		if n.naiveObsMax(os, oe)+1 > n.cfg.ObservationBeds {
			return errf(CodeObservationFull,
				fmt.Sprintf("留观区间 [%d,%d) 内留观人数将达上限 %d", os, oe, n.cfg.ObservationBeds))
		}
	}
	return nil
}

func (n *naiveSystem) hold(b *naiveBooking) {
	d := n.devices[b.deviceID]
	et := n.examTypes[b.examTypeID]
	d.occ = append(d.occ, naiveInterval{s: b.start, e: b.start + et.occupy + et.clean, id: b.id})
	if et.enhanced && n.cfg.ObservationDuration > 0 {
		n.obs = append(n.obs, naiveInterval{s: b.start + et.occupy, e: b.start + et.occupy + n.cfg.ObservationDuration, id: b.id})
	}
	b.held = true
}

func (n *naiveSystem) release(b *naiveBooking) {
	d := n.devices[b.deviceID]
	et := n.examTypes[b.examTypeID]
	end := b.start + et.occupy + et.clean
	for i, iv := range d.occ {
		if iv.s == b.start && iv.e == end && iv.id == b.id {
			d.occ = append(d.occ[:i], d.occ[i+1:]...)
			break
		}
	}
	if et.enhanced && n.cfg.ObservationDuration > 0 {
		os, oe := b.start+et.occupy, b.start+et.occupy+n.cfg.ObservationDuration
		for i, iv := range n.obs {
			if iv.s == os && iv.e == oe && iv.id == b.id {
				n.obs = append(n.obs[:i], n.obs[i+1:]...)
				break
			}
		}
	}
	b.held = false
}

func (n *naiveSystem) Book(now int, id, patientID, examTypeID, deviceID string, start int) *Error {
	if !validID(id) || !validID(patientID) || !validID(examTypeID) || !validID(deviceID) ||
		!validTime(now) || !validTime(start) {
		return errf(CodeInvalidParam, "标识为空或时间越界")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	if _, dup := n.bookings[id]; dup {
		return errf(CodeStateMismatch, "预约标识已存在: "+id)
	}
	p, ok := n.patients[patientID]
	if !ok {
		return errf(CodeNotFound, "患者不存在: "+patientID)
	}
	et, ok := n.examTypes[examTypeID]
	if !ok {
		return errf(CodeNotFound, "检查类型不存在: "+examTypeID)
	}
	d, ok := n.devices[deviceID]
	if !ok {
		return errf(CodeNotFound, "设备不存在: "+deviceID)
	}
	if err := n.validateSlot(p, et, d, start); err != nil {
		return err
	}
	b := &naiveBooking{
		id:         id,
		patientID:  patientID,
		examTypeID: examTypeID,
		deviceID:   deviceID,
		start:      start,
		state:      StateAccepted,
	}
	n.bookings[id] = b
	n.hold(b)
	n.advance(now)
	return nil
}

func (n *naiveSystem) RegisterHydration(now int, bookingID string, start int) *Error {
	return n.registerPrep(now, bookingID, start, true)
}

func (n *naiveSystem) RegisterPremed(now int, bookingID string, start int) *Error {
	return n.registerPrep(now, bookingID, start, false)
}

func (n *naiveSystem) registerPrep(now int, bookingID string, start int, hydration bool) *Error {
	kind := "预处理"
	if hydration {
		kind = "水化"
	}
	if !validID(bookingID) || !validTime(now) || !validTime(start) {
		return errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if start > now {
		return errf(CodeInvalidParam, kind+"开始时刻不得晚于 now")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	b, ok := n.bookings[bookingID]
	if !ok {
		return errf(CodeNotFound, "预约不存在: "+bookingID)
	}
	if b.state != StateAccepted {
		return errf(CodeStateMismatch, fmt.Sprintf("预约 %s 处于%s，不能登记%s", b.id, b.state, kind))
	}
	if hydration {
		b.hydration = &start
	} else {
		b.premed = &start
	}
	n.advance(now)
	return nil
}

func (n *naiveSystem) CheckIn(now int, bookingID string) (CheckInOutcome, *Error) {
	if !validID(bookingID) || !validTime(now) {
		return CheckInOutcome{}, errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if e := n.checkClock(now); e != nil {
		return CheckInOutcome{}, e
	}
	b, ok := n.bookings[bookingID]
	if !ok {
		return CheckInOutcome{}, errf(CodeNotFound, "预约不存在: "+bookingID)
	}
	if b.state != StateAccepted {
		return CheckInOutcome{}, errf(CodeStateMismatch,
			fmt.Sprintf("预约 %s 处于%s，不能签到", b.id, b.state))
	}
	if now < b.start-30 || now > b.start+15 {
		return CheckInOutcome{}, errf(CodeCheckinWindow,
			fmt.Sprintf("签到时刻 %d 不在窗口 [%d,%d] 内", now, b.start-30, b.start+15))
	}
	n.advance(now)
	et := n.examTypes[b.examTypeID]
	p := n.patients[b.patientID]
	fail := func(code Code, detail string) (CheckInOutcome, *Error) {
		b.state = StateNeedsReschedule
		n.release(b)
		return CheckInOutcome{State: StateNeedsReschedule, Reason: code, Detail: detail}, nil
	}
	if et.enhanced {
		code, detail, hydrationNeeded := n.evalRenal(p, b.start)
		if code != OK {
			return fail(code, detail)
		}
		if hydrationNeeded {
			if b.hydration == nil || *b.hydration > b.start-n.cfg.HydrationLead {
				return fail(CodeNotHydrated,
					fmt.Sprintf("水化开始时刻须不晚于 %d", b.start-n.cfg.HydrationLead))
			}
		}
		if p.allergy {
			if b.premed == nil || *b.premed > b.start-n.cfg.PremedLead {
				return fail(CodeNotPremedicated,
					fmt.Sprintf("预处理开始时刻须不晚于 %d", b.start-n.cfg.PremedLead))
			}
		}
	}
	b.state = StateCheckedIn
	return CheckInOutcome{State: StateCheckedIn, Reason: OK}, nil
}

func (n *naiveSystem) Reschedule(now int, bookingID string, newStart *int, newDeviceID string) *Error {
	if !validID(bookingID) || !validTime(now) {
		return errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if newStart == nil && newDeviceID == "" {
		return errf(CodeInvalidParam, "新的开始时刻与新的设备至少给出一个")
	}
	if newStart != nil && !validTime(*newStart) {
		return errf(CodeInvalidParam, "新的开始时刻越界")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	b, ok := n.bookings[bookingID]
	if !ok {
		return errf(CodeNotFound, "预约不存在: "+bookingID)
	}
	var nd *naiveDevice
	if newDeviceID != "" {
		nd, ok = n.devices[newDeviceID]
		if !ok {
			return errf(CodeNotFound, "设备不存在: "+newDeviceID)
		}
	}
	if b.state != StateAccepted && b.state != StateNeedsReschedule {
		return errf(CodeStateMismatch, fmt.Sprintf("预约 %s 处于%s，不能改约", b.id, b.state))
	}
	p := n.patients[b.patientID]
	et := n.examTypes[b.examTypeID]
	start := b.start
	if newStart != nil {
		start = *newStart
	}
	d := n.devices[b.deviceID]
	if nd != nil {
		d = nd
	}
	wasHeld := b.held
	if wasHeld {
		n.release(b)
	}
	if err := n.validateSlot(p, et, d, start); err != nil {
		if wasHeld {
			n.hold(b)
		}
		return err
	}
	b.start = start
	b.deviceID = d.id
	n.hold(b)
	b.state = StateAccepted
	b.hydration = nil
	b.premed = nil
	n.advance(now)
	return nil
}

func (n *naiveSystem) Cancel(now int, bookingID string) *Error {
	if !validID(bookingID) || !validTime(now) {
		return errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if e := n.checkClock(now); e != nil {
		return e
	}
	b, ok := n.bookings[bookingID]
	if !ok {
		return errf(CodeNotFound, "预约不存在: "+bookingID)
	}
	switch b.state {
	case StateAccepted:
		n.release(b)
		b.state = StateCancelled
	case StateNeedsReschedule:
		b.state = StateCancelled
	default:
		return errf(CodeStateMismatch, fmt.Sprintf("预约 %s 处于%s，不能取消", b.id, b.state))
	}
	n.advance(now)
	return nil
}
