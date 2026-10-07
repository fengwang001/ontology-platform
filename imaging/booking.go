package imaging

import "fmt"

// booking.go 实现预约的生命周期操作：受理、水化/预处理登记、
// 签到复核、改约与取消。

// Book 受理一次预约。受理时只判定：设备类别、植入物兼容、
// 结果时效与肾功能下限、设备区间（含清洁）与质控、留观位容量。
// 水化与预处理不在受理时要求。
func (s *System) Book(now int, id, patientID, examTypeID, deviceID string, start int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validID(patientID) || !validID(examTypeID) || !validID(deviceID) ||
		!validTime(now) || !validTime(start) {
		return errf(CodeInvalidParam, "标识为空或时间越界")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	if _, dup := s.bookings[id]; dup {
		return errf(CodeStateMismatch, "预约标识已存在: "+id)
	}
	p, ok := s.patients[patientID]
	if !ok {
		return errf(CodeNotFound, "患者不存在: "+patientID)
	}
	et, ok := s.examTypes[examTypeID]
	if !ok {
		return errf(CodeNotFound, "检查类型不存在: "+examTypeID)
	}
	d, ok := s.devices[deviceID]
	if !ok {
		return errf(CodeNotFound, "设备不存在: "+deviceID)
	}
	if err := s.validateSlot(p, et, d, start); err != nil {
		return err
	}
	b := &booking{
		id:         id,
		patientID:  patientID,
		examTypeID: examTypeID,
		deviceID:   deviceID,
		start:      start,
		state:      StateAccepted,
	}
	s.bookings[id] = b
	s.hold(b)
	s.advance(now)
	return nil
}

// RegisterHydration 为已受理预约登记水化开始时刻（不晚于 now）。
// 重复登记覆盖先前记录。
func (s *System) RegisterHydration(now int, bookingID string, start int) *Error {
	return s.registerPrep(now, bookingID, start, true)
}

// RegisterPremed 为已受理预约登记对比剂过敏预处理开始时刻（不晚于 now）。
// 重复登记覆盖先前记录。
func (s *System) RegisterPremed(now int, bookingID string, start int) *Error {
	return s.registerPrep(now, bookingID, start, false)
}

func (s *System) registerPrep(now int, bookingID string, start int, hydration bool) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if e := s.checkClock(now); e != nil {
		return e
	}
	b, ok := s.bookings[bookingID]
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
	s.advance(now)
	return nil
}

// CheckIn 签到。窗口为 [开始-30, 开始+15]（均含）；窗口外报签到窗口不符，
// 操作被拒绝，不改变状态与时钟。窗口内签到被接受：以签到时刻的最新肾功能
// 结果按预约开始时刻重新判定时效与下限，并核对水化与预处理；全部通过则转
// 已签到，任一不通过则转需改期并立即释放设备占用与留观占用，按次序报告
// 第一个原因。
func (s *System) CheckIn(now int, bookingID string) (CheckInOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(bookingID) || !validTime(now) {
		return CheckInOutcome{}, errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if e := s.checkClock(now); e != nil {
		return CheckInOutcome{}, e
	}
	b, ok := s.bookings[bookingID]
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
	// 窗口内：签到被接受，改变状态与时钟。
	s.advance(now)
	et := s.examTypes[b.examTypeID]
	p := s.patients[b.patientID]
	fail := func(code Code, detail string) (CheckInOutcome, *Error) {
		b.state = StateNeedsReschedule
		s.release(b)
		return CheckInOutcome{State: StateNeedsReschedule, Reason: code, Detail: detail}, nil
	}
	if et.enhanced {
		code, detail, hydrationNeeded := s.evalRenal(p, b.start)
		if code != OK {
			return fail(code, detail)
		}
		if hydrationNeeded {
			if b.hydration == nil || *b.hydration > b.start-s.cfg.HydrationLead {
				return fail(CodeNotHydrated,
					fmt.Sprintf("水化开始时刻须不晚于 %d", b.start-s.cfg.HydrationLead))
			}
		}
		if p.allergy {
			if b.premed == nil || *b.premed > b.start-s.cfg.PremedLead {
				return fail(CodeNotPremedicated,
					fmt.Sprintf("预处理开始时刻须不晚于 %d", b.start-s.cfg.PremedLead))
			}
		}
	}
	b.state = StateCheckedIn
	return CheckInOutcome{State: StateCheckedIn, Reason: OK}, nil
}

// Reschedule 原子地改约到新的开始时刻（newStart 非 nil）或新的设备
// （newDeviceID 非空）。旧占用不阻挡新占用；全部受理条件按新安排重新判定，
// 失败则保持原约且时钟不变。成功后已登记的水化与预处理作废。
func (s *System) Reschedule(now int, bookingID string, newStart *int, newDeviceID string) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(bookingID) || !validTime(now) {
		return errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if newStart == nil && newDeviceID == "" {
		return errf(CodeInvalidParam, "新的开始时刻与新的设备至少给出一个")
	}
	if newStart != nil && !validTime(*newStart) {
		return errf(CodeInvalidParam, "新的开始时刻越界")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	b, ok := s.bookings[bookingID]
	if !ok {
		return errf(CodeNotFound, "预约不存在: "+bookingID)
	}
	var nd *device
	if newDeviceID != "" {
		nd, ok = s.devices[newDeviceID]
		if !ok {
			return errf(CodeNotFound, "设备不存在: "+newDeviceID)
		}
	}
	if b.state != StateAccepted && b.state != StateNeedsReschedule {
		return errf(CodeStateMismatch, fmt.Sprintf("预约 %s 处于%s，不能改约", b.id, b.state))
	}
	p := s.patients[b.patientID]
	et := s.examTypes[b.examTypeID]
	start := b.start
	if newStart != nil {
		start = *newStart
	}
	d := s.devices[b.deviceID]
	if nd != nil {
		d = nd
	}
	// 原子换约：先摘除旧占用（需改期状态的预约占用已释放），
	// 判定失败则恢复原占用，预约保持不变。
	wasHeld := b.held
	if wasHeld {
		s.release(b)
	}
	if err := s.validateSlot(p, et, d, start); err != nil {
		if wasHeld {
			s.hold(b)
		}
		return err
	}
	b.start = start
	b.deviceID = d.id
	s.hold(b)
	b.state = StateAccepted
	b.hydration = nil
	b.premed = nil
	s.advance(now)
	return nil
}

// Cancel 取消预约，释放设备占用与留观占用。
// 已签到的预约不可取消，报状态不符。
func (s *System) Cancel(now int, bookingID string) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(bookingID) || !validTime(now) {
		return errf(CodeInvalidParam, "预约标识为空或时间越界")
	}
	if e := s.checkClock(now); e != nil {
		return e
	}
	b, ok := s.bookings[bookingID]
	if !ok {
		return errf(CodeNotFound, "预约不存在: "+bookingID)
	}
	switch b.state {
	case StateAccepted:
		s.release(b)
		b.state = StateCancelled
	case StateNeedsReschedule:
		// 占用在签到复核失败时已释放。
		b.state = StateCancelled
	default:
		return errf(CodeStateMismatch, fmt.Sprintf("预约 %s 处于%s，不能取消", b.id, b.state))
	}
	s.advance(now)
	return nil
}

// Stats 汇总数据结构访问计数，用于性能可验证性测试。
type Stats struct {
	DeviceVisits int64 // 设备占用树访问结点数（全院合计）
	ObsVisits    int64 // 留观事件树访问结点数
}

// Stats 返回自上次 ResetStats 以来的累计访问计数。
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st Stats
	for _, d := range s.devices {
		st.DeviceVisits += d.occ.visits
	}
	st.ObsVisits = s.obs.visits
	return st
}

// ResetStats 清零访问计数。
func (s *System) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		d.occ.visits = 0
	}
	s.obs.visits = 0
}
