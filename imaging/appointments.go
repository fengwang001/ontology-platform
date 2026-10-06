package imaging

// Book 受理一次预约。
// 判定优先级：参数非法、时钟回退、对象不存在、设备类别不符、植入物不兼容、
// 结果缺失或过期、肾功能不足、设备时段冲突、质控时段冲突、留观位不足。
// 失败不改变任何状态与时钟。
func (h *Hospital) Book(req BookRequest) error {
	if !nonemptyID(req.ID) || !nonemptyID(req.PatientID) || !nonemptyID(req.ExamTypeID) ||
		!nonemptyID(req.DeviceID) || !validTime(req.Start) || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if err := h.begin(req.Now); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			h.abort()
		}
	}()
	if _, exists := h.appts[req.ID]; exists {
		return ErrInvalidArgument
	}
	p, ok := h.patients[req.PatientID]
	if !ok {
		return ErrNotFound
	}
	ex, ok := h.exams[req.ExamTypeID]
	if !ok {
		return ErrNotFound
	}
	d, ok := h.devices[req.DeviceID]
	if !ok {
		return ErrNotFound
	}
	if err := h.checkArrangement(p, ex, d, req.Start, 0, nil); err != nil {
		return err
	}
	h.idSeq++
	a := h.newAppointment(req.ID, p, ex, d, req.Start, h.idSeq)
	a.treeID = h.idSeq
	a.status = StatusBooked
	h.appts[a.id] = a
	d.tree.insert(h.idSeq, a.start, a.occupEnd)
	if ex.enhanced {
		h.addObservation(a.examEnd, a.obsEnd)
	}
	h.commit(req.Now)
	committed = true
	return nil
}

// checkArrangement 按统一优先级判定给定安排是否可行（不落任何状态）。
// skipApt 非 nil 表示改约：其旧设备占用与旧留观占用不阻挡新安排。
func (h *Hospital) checkArrangement(p *patient, ex *examType, d *device, start int, skipID uint64, skipApt *appointment) error {
	if ex.class != d.class {
		return ErrDeviceClassMismatch
	}
	if ex.class == ClassMR && !p.noImplant && d.fieldStrength > p.maxFieldStrength {
		return ErrImplantIncompatible
	}
	cleaning := h.cfg.CleaningMinutes[ex.class]
	examEnd := start + ex.duration
	occupEnd := examEnd + cleaning
	if occupEnd > maxTime {
		return ErrInvalidArgument
	}
	if ex.enhanced {
		obsEnd := examEnd + h.cfg.ObservationMinutes
		if obsEnd > maxTime {
			return ErrInvalidArgument
		}
		dec := h.assessKidney(p, start)
		if dec.err != nil {
			return dec.err
		}
	}
	if d.tree.overlapsAny(start, occupEnd, skipID) {
		return ErrDeviceBusy
	}
	if qcConflict(d.qcs, start, occupEnd) {
		return ErrQCConflict
	}
	if ex.enhanced {
		// 临时扣除自身旧留观占用后检查新窗口：将旧区间从计数中“借位”等价处理。
		if !h.observationOK(examEnd, examEnd+h.cfg.ObservationMinutes, skipApt) {
			return ErrObservationFull
		}
	}
	return nil
}

// observationOK 判定加入新区间 [s,e) 是否安全；skipApt 的旧留观区间视为已移除。
func (h *Hospital) observationOK(s, e int, skipApt *appointment) bool {
	baseline := func(t int) int {
		c := h.countAt(t)
		if skipApt != nil && h.exams[skipApt.examTypeID].enhanced &&
			skipApt.obsEnd > skipApt.examEnd &&
			skipApt.examEnd <= t && t < skipApt.obsEnd {
			c--
		}
		return c
	}
	var events []capacityEvent
	addRange := func(ms *multiSet, delta int) {
		for _, t := range ms.rangeKeys(s, e-1) {
			events = append(events, capacityEvent{t, delta})
		}
	}
	addRange(h.obs.starts, 1)
	addRange(h.obs.ends, -1)
	// 若跳过自身旧区间，其事件在窗口内则抵消。
	if skipApt != nil && h.exams[skipApt.examTypeID].enhanced && skipApt.obsEnd > skipApt.examEnd {
		if skipApt.examEnd >= s && skipApt.examEnd < e {
			events = append(events, capacityEvent{skipApt.examEnd, -1})
		}
		if skipApt.obsEnd >= s && skipApt.obsEnd < e {
			events = append(events, capacityEvent{skipApt.obsEnd, 1})
		}
	}
	return sweepCapacity(events, baseline(s-1)+1, h.cfg.ObservationCapacity)
}

func (h *Hospital) newAppointment(id string, p *patient, ex *examType, d *device, start int, treeID uint64) *appointment {
	cleaning := h.cfg.CleaningMinutes[ex.class]
	a := &appointment{
		id:         id,
		patientID:  p.id,
		examTypeID: ex.id,
		deviceID:   d.id,
		start:      start,
		examEnd:    start + ex.duration,
		occupEnd:   start + ex.duration + cleaning,
	}
	if ex.enhanced {
		a.obsEnd = a.examEnd + h.cfg.ObservationMinutes
	}
	return a
}

// RegisterHydration 为已受理（未签到、未需改期、未取消）的预约登记水化。
func (h *Hospital) RegisterHydration(req HydrationRequest) error {
	return h.registerPreparation(req, true)
}

// RegisterPremedication 为已受理预约登记对比剂过敏预处理。
func (h *Hospital) RegisterPremedication(req PremedicationRequest) error {
	return h.registerPreparation(req, false)
}

func (h *Hospital) registerPreparation(req HydrationRequest, hydration bool) error {
	if !nonemptyID(req.AppointmentID) || !validTime(req.StartAt) || !validTime(req.Now) ||
		req.StartAt > req.Now {
		return ErrInvalidArgument
	}
	if err := h.begin(req.Now); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			h.abort()
		}
	}()
	a, ok := h.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status != StatusBooked {
		return ErrInvalidState
	}
	if hydration {
		a.hydrationAt = req.StartAt
		a.hydrationDone = true
	} else {
		a.premedAt = req.StartAt
		a.premedDone = true
	}
	h.commit(req.Now)
	committed = true
	return nil
}

// CheckIn 签到：窗口外拒绝且不改变状态/时钟；窗口内做准入复核。
func (h *Hospital) CheckIn(req CheckInRequest) error {
	if !nonemptyID(req.AppointmentID) || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if err := h.begin(req.Now); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			h.abort()
		}
	}()
	a, ok := h.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status != StatusBooked {
		return ErrInvalidState
	}
	// [start-30, start+15] 闭区间。
	if req.Now < a.start-30 || req.Now > a.start+15 {
		return ErrCheckinWindow
	}
	p := h.patients[a.patientID]
	ex := h.exams[a.examTypeID]
	if ex.enhanced {
		// 以签到时刻最新结果，按预约开始时刻重新判定。
		dec := h.assessKidney(p, a.start)
		if dec.err != nil {
			h.failCheckin(a)
			h.commit(req.Now)
			committed = true
			return dec.err
		}
		// 须水化：已登记且开始时刻不晚于 start - 提前量（恰取等满足）。
		if dec.needHydration {
			if !a.hydrationDone || a.hydrationAt > a.start-h.cfg.HydrationLead {
				h.failCheckin(a)
				h.commit(req.Now)
				committed = true
				return ErrNotHydrated
			}
		}
		// 过敏预处理。
		if p.allergic {
			if !a.premedDone || a.premedAt > a.start-h.cfg.PremedicationLead {
				h.failCheckin(a)
				h.commit(req.Now)
				committed = true
				return ErrNotPremedicated
			}
		}
	}
	a.status = StatusCheckedIn
	h.commit(req.Now)
	committed = true
	return nil
}

// failCheckin 将预约置为需改期并立即释放设备与留观占用。
func (h *Hospital) failCheckin(a *appointment) {
	a.status = StatusNeedsReschedule
	if d, ok := h.devices[a.deviceID]; ok {
		d.tree.remove(a.treeID, a.start, a.occupEnd)
	}
	if h.exams[a.examTypeID].enhanced {
		h.removeObservation(a.examEnd, a.obsEnd)
	}
}

// Reschedule 原子改约；失败保持原约。成功后水化/预处理登记作废。
func (h *Hospital) Reschedule(req RescheduleRequest) error {
	if !nonemptyID(req.AppointmentID) || !validTime(req.NewStart) || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	targetDevice := req.NewDeviceID
	if err := h.begin(req.Now); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			h.abort()
		}
	}()
	a, ok := h.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status != StatusBooked && a.status != StatusNeedsReschedule {
		return ErrInvalidState
	}
	p := h.patients[a.patientID]
	ex := h.exams[a.examTypeID]
	if targetDevice == "" {
		targetDevice = a.deviceID
	}
	d, ok := h.devices[targetDevice]
	if !ok {
		return ErrNotFound
	}
	// 全部条件按新安排重新判定，旧占用（含自身）不阻挡。
	if err := h.checkArrangement(p, ex, d, req.NewStart, a.treeID, a); err != nil {
		return err
	}
	// 条件通过：原子切换。先摘旧占用，再挂新占用。
	oldDevice := h.devices[a.deviceID]
	oldEnhanced := ex.enhanced
	oldStart, oldOccup := a.start, a.occupEnd
	oldExamEnd, oldObsEnd := a.examEnd, a.obsEnd
	cleaning := h.cfg.CleaningMinutes[ex.class]
	oldDevice.tree.remove(a.treeID, oldStart, oldOccup)
	if oldEnhanced && a.status == StatusBooked {
		// 需改期状态的预约此前已释放占用，不得重复释放。
		h.removeObservation(oldExamEnd, oldObsEnd)
	}
	a.deviceID = d.id
	a.start = req.NewStart
	a.examEnd = req.NewStart + ex.duration
	a.occupEnd = a.examEnd + cleaning
	a.obsEnd = 0
	if oldEnhanced {
		a.obsEnd = a.examEnd + h.cfg.ObservationMinutes
		h.addObservation(a.examEnd, a.obsEnd)
	}
	d.tree.insert(a.treeID, a.start, a.occupEnd)
	a.status = StatusBooked
	a.hydrationDone = false
	a.hydrationAt = 0
	a.premedDone = false
	a.premedAt = 0
	h.commit(req.Now)
	committed = true
	return nil
}

// Cancel 取消预约并释放占用；已签到不可取消。
func (h *Hospital) Cancel(req CancelRequest) error {
	if !nonemptyID(req.AppointmentID) || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if err := h.begin(req.Now); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			h.abort()
		}
	}()
	a, ok := h.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status == StatusCheckedIn {
		return ErrInvalidState
	}
	if a.status == StatusCancelled {
		return ErrInvalidState
	}
	if a.status == StatusBooked {
		if d, ok := h.devices[a.deviceID]; ok {
			d.tree.remove(a.treeID, a.start, a.occupEnd)
		}
		if h.exams[a.examTypeID].enhanced {
			h.removeObservation(a.examEnd, a.obsEnd)
		}
	}
	a.status = StatusCancelled
	h.commit(req.Now)
	committed = true
	return nil
}
