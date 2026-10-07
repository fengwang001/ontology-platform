package imaging

import (
	"fmt"
	"sync"
)

// system.go 实现预约与对比剂准入系统的全部对外操作。
//
// 并发模型：所有操作在单个互斥锁下串行执行，因此任意并发调用的结果
// 必然等价于某个串行顺序；被拒绝的操作在持锁期间不产生任何写入，
// 状态与逻辑时钟保持不变。

type device struct {
	id       string
	category DeviceCategory
	field    int // 磁共振场强；CT 恒为 0
	qc       [][2]int
	occ      intervalSet
}

type examType struct {
	id       string
	category DeviceCategory
	occupy   int
	enhanced bool
	clean    int
}

type renalResult struct {
	value      int
	sampleTime int
	seq        int64 // 登记序号，采样时刻并列时认登记在后者
}

type patient struct {
	id         string
	highRisk   bool
	hasImplant bool
	maxField   int
	allergy    bool
	renal      *renalResult // 仅保留采样时刻最新者
}

type booking struct {
	id         string
	patientID  string
	examTypeID string
	deviceID   string
	start      int
	state      BookingState
	held       bool // 是否当前持有设备占用与留观占用
	hydration  *int // 已登记的水化开始时刻
	premed     *int // 已登记的预处理开始时刻
}

// System 是系统入口，所有方法可并发调用。
type System struct {
	mu        sync.Mutex
	cfg       Config
	now       int
	started   bool
	seq       int64
	devices   map[string]*device
	examTypes map[string]*examType
	patients  map[string]*patient
	bookings  map[string]*booking
	obs       obsTree
}

// NewSystem 校验配置并构造系统。
func NewSystem(cfg Config) (*System, *Error) {
	if cfg.RenalValidNormal < 0 || cfg.RenalValidHighRisk < 0 ||
		cfg.HydrationLead < 0 || cfg.PremedLead < 0 ||
		cfg.ObservationBeds < 0 || cfg.ObservationDuration < 0 {
		return nil, errf(CodeInvalidParam, "配置中的时长与容量不得为负")
	}
	if cfg.RenalLower > cfg.RenalUpper {
		return nil, errf(CodeInvalidParam, "肾功能下限不得大于上限")
	}
	return &System{
		cfg:       cfg,
		devices:   map[string]*device{},
		examTypes: map[string]*examType{},
		patients:  map[string]*patient{},
		bookings:  map[string]*booking{},
	}, nil
}

func validID(id string) bool { return id != "" }
func validTime(t int) bool   { return t >= 0 && t <= MaxTime }
func positive(d int) bool    { return d > 0 }

// checkClock 校验逻辑时钟；被拒绝的操作不得改变时钟。
func (s *System) checkClock(now int) *Error {
	if s.started && now < s.now {
		return errf(CodeClockRollback, fmt.Sprintf("now=%d 小于上次被接受操作的 now=%d", now, s.now))
	}
	return nil
}

func (s *System) advance(now int) {
	s.now = now
	s.started = true
}

// RegisterDevice 登记设备。磁共振设备须登记正整数场强；CT 场强须为 0。
func (s *System) RegisterDevice(now int, id string, category DeviceCategory, field int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) || (category != CT && category != MR) {
		return errf(CodeInvalidParam, "设备标识为空、时间越界或类别非法")
	}
	if category == MR && !positive(field) {
		return errf(CodeInvalidParam, "磁共振设备场强须为正整数")
	}
	if category == CT && field != 0 {
		return errf(CodeInvalidParam, "CT 设备不登记场强")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	if _, dup := s.devices[id]; dup {
		return errf(CodeStateMismatch, "设备标识已存在: "+id)
	}
	s.devices[id] = &device{id: id, category: category, field: field}
	s.advance(now)
	return nil
}

// RegisterQC 为设备登记每日重复的质控时段 [startMin,endMin)，不跨日。
func (s *System) RegisterQC(now int, deviceID string, startMin, endMin int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(deviceID) || !validTime(now) || startMin < 0 || startMin >= endMin || endMin > DayMinutes {
		return errf(CodeInvalidParam, "质控时段须满足 0<=start<end<=1440")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	d, ok := s.devices[deviceID]
	if !ok {
		return errf(CodeNotFound, "设备不存在: "+deviceID)
	}
	d.qc = append(d.qc, [2]int{startMin, endMin})
	s.advance(now)
	return nil
}

// RegisterExamType 登记检查类型。
func (s *System) RegisterExamType(now int, id string, category DeviceCategory, occupy int, enhanced bool, clean int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) || (category != CT && category != MR) ||
		!positive(occupy) || !positive(clean) {
		return errf(CodeInvalidParam, "检查类型参数非法（标识/类别/时长）")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	if _, dup := s.examTypes[id]; dup {
		return errf(CodeStateMismatch, "检查类型标识已存在: "+id)
	}
	s.examTypes[id] = &examType{id: id, category: category, occupy: occupy, enhanced: enhanced, clean: clean}
	s.advance(now)
	return nil
}

// RegisterPatient 登记患者。maxField 仅在有植入物时有意义（须为正整数）。
func (s *System) RegisterPatient(now int, id string, highRisk, hasImplant bool, maxField int, contrastAllergy bool) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) {
		return errf(CodeInvalidParam, "患者标识为空或时间越界")
	}
	if hasImplant && !positive(maxField) {
		return errf(CodeInvalidParam, "有植入物时允许的最大场强须为正整数")
	}
	if !hasImplant && maxField != 0 {
		return errf(CodeInvalidParam, "无植入物时最大场强须为 0")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	if _, dup := s.patients[id]; dup {
		return errf(CodeStateMismatch, "患者标识已存在: "+id)
	}
	s.patients[id] = &patient{id: id, highRisk: highRisk, hasImplant: hasImplant, maxField: maxField, allergy: contrastAllergy}
	s.advance(now)
	return nil
}

// RegisterRenal 登记肾功能结果；后登记覆盖先登记，只认采样时刻最晚者，
// 采样时刻并列时认登记在后者。
func (s *System) RegisterRenal(now int, patientID string, value, sampleTime int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(patientID) || !validTime(now) || !validTime(sampleTime) {
		return errf(CodeInvalidParam, "患者标识为空或时间越界")
	}
	if sampleTime > now {
		return errf(CodeInvalidParam, "采样时刻不得晚于 now")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	p, ok := s.patients[patientID]
	if !ok {
		return errf(CodeNotFound, "患者不存在: "+patientID)
	}
	s.seq++
	if p.renal == nil || sampleTime >= p.renal.sampleTime {
		p.renal = &renalResult{value: value, sampleTime: sampleTime, seq: s.seq}
	}
	s.advance(now)
	return nil
}

// evalRenal 以 start 为判定时刻评估患者最新肾功能结果。
// 返回 (错误码, 是否须水化)；错误码为 OK 时第二个返回值有效。
func (s *System) evalRenal(p *patient, start int) (Code, string, bool) {
	r := p.renal
	if r == nil {
		return CodeRenalMissingOrExpired, "无肾功能结果", false
	}
	if r.sampleTime > start {
		return CodeRenalMissingOrExpired,
			fmt.Sprintf("最新结果采样时刻 %d 晚于判定时刻 %d", r.sampleTime, start), false
	}
	validity := s.cfg.RenalValidNormal
	if p.highRisk {
		validity = s.cfg.RenalValidHighRisk
	}
	if start-r.sampleTime > validity {
		return CodeRenalMissingOrExpired,
			fmt.Sprintf("结果已超期：%d-%d=%d > 有效期 %d", start, r.sampleTime, start-r.sampleTime, validity), false
	}
	if r.value < s.cfg.RenalLower {
		return CodeRenalInsufficient,
			fmt.Sprintf("结果数值 %d 低于下限 %d", r.value, s.cfg.RenalLower), false
	}
	return OK, "", r.value < s.cfg.RenalUpper
}

// qcConflict 判定占用区间 [s,e) 是否与设备任一天的质控时段重叠。
func qcConflict(d *device, s, e int) bool {
	if len(d.qc) == 0 {
		return false
	}
	first := s / DayMinutes
	last := (e - 1) / DayMinutes
	for day := first; day <= last; day++ {
		base := day * DayMinutes
		for _, w := range d.qc {
			if base+w[0] < e && s < base+w[1] {
				return true
			}
		}
	}
	return false
}

// validateSlot 按错误优先级判定 (患者, 检查类型, 设备, 开始时刻) 组合是否可受理。
// 调用方须已完成参数、时钟、存在性与状态校验。
func (s *System) validateSlot(p *patient, et *examType, d *device, start int) *Error {
	if et.category != d.category {
		return errf(CodeDeviceCategoryMismatch,
			fmt.Sprintf("检查类型 %s 要求 %s，设备 %s 为 %s", et.id, et.category, d.id, d.category))
	}
	if d.category == MR && p.hasImplant && d.field > p.maxField {
		return errf(CodeImplantIncompatible,
			fmt.Sprintf("设备场强 %d 超过患者允许的最大场强 %d", d.field, p.maxField))
	}
	if et.enhanced {
		if code, detail, _ := s.evalRenal(p, start); code != OK {
			return errf(code, detail)
		}
	}
	end := start + et.occupy + et.clean
	if d.occ.overlaps(start, end) {
		return errf(CodeDeviceConflict,
			fmt.Sprintf("设备 %s 在 [%d,%d) 已有占用", d.id, start, end))
	}
	if qcConflict(d, start, end) {
		return errf(CodeQCConflict,
			fmt.Sprintf("设备 %s 的占用区间 [%d,%d) 与每日质控时段重叠", d.id, start, end))
	}
	if et.enhanced && s.cfg.ObservationDuration > 0 {
		os, oe := start+et.occupy, start+et.occupy+s.cfg.ObservationDuration
		if !s.obs.canAdd(os, oe, s.cfg.ObservationBeds) {
			return errf(CodeObservationFull,
				fmt.Sprintf("留观区间 [%d,%d) 内留观人数将达上限 %d", os, oe, s.cfg.ObservationBeds))
		}
	}
	return nil
}

// hold 为预约登记设备占用与留观占用。
func (s *System) hold(b *booking) {
	d := s.devices[b.deviceID]
	et := s.examTypes[b.examTypeID]
	d.occ.insert(b.start, b.start+et.occupy+et.clean, b.id)
	if et.enhanced && s.cfg.ObservationDuration > 0 {
		s.obs.add(b.start+et.occupy, 1)
		s.obs.add(b.start+et.occupy+s.cfg.ObservationDuration, -1)
	}
	b.held = true
}

// release 释放预约的设备占用与留观占用。
func (s *System) release(b *booking) {
	d := s.devices[b.deviceID]
	et := s.examTypes[b.examTypeID]
	d.occ.remove(b.start)
	if et.enhanced && s.cfg.ObservationDuration > 0 {
		s.obs.add(b.start+et.occupy, -1)
		s.obs.add(b.start+et.occupy+s.cfg.ObservationDuration, 1)
	}
	b.held = false
}
