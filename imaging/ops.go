package imaging

// begin 是所有写操作的统一入口：加锁，校验时钟单调。
// 用法：
//
//	committed := false
//	if err := h.begin(now); err != nil { return err }
//	defer func() { if !committed { h.abort() } }()
//	... 校验通过后修改状态，调用 h.commit，置 committed = true
//
// 任何返回非 nil 错误的路径都不得修改状态与时钟。
func (h *Hospital) begin(now int) error {
	h.mu.Lock()
	if now < h.lastNow {
		h.mu.Unlock()
		return ErrClockRollback
	}
	return nil
}

func (h *Hospital) commit(now int) {
	h.lastNow = now
	h.accepted++
	h.mu.Unlock()
}

func (h *Hospital) abort() { h.mu.Unlock() }

// RegisterDevice 登记设备。重复 ID 为参数非法。
func (h *Hospital) RegisterDevice(req RegisterDeviceRequest) error {
	if !nonemptyID(req.ID) || (req.Class != ClassCT && req.Class != ClassMR) {
		return ErrInvalidArgument
	}
	if req.Class == ClassMR && req.FieldStrength <= 0 {
		return ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.devices[req.ID]; exists {
		return ErrInvalidArgument
	}
	d := &device{id: req.ID, class: req.Class, tree: &intervalTree{}}
	if req.Class == ClassMR {
		d.fieldStrength = req.FieldStrength
	}
	h.devices[req.ID] = d
	return nil
}

// AddQC 为设备登记每日重复的日内质控时段（不跨日）。
func (h *Hospital) AddQC(req AddQCRequest) error {
	iv := req.Interval
	if !nonemptyID(req.DeviceID) || iv.Start < 0 || iv.End <= iv.Start || iv.End > dayMin {
		return ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.devices[req.DeviceID]
	if !ok {
		return ErrNotFound
	}
	d.qcs = append(d.qcs, iv)
	return nil
}

// RegisterExamType 登记检查类型。
func (h *Hospital) RegisterExamType(req RegisterExamTypeRequest) error {
	if !nonemptyID(req.ID) || req.Duration <= 0 ||
		(req.Class != ClassCT && req.Class != ClassMR) {
		return ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.exams[req.ID]; exists {
		return ErrInvalidArgument
	}
	h.exams[req.ID] = &examType{id: req.ID, class: req.Class, duration: req.Duration, enhanced: req.Enhanced}
	return nil
}

// RegisterPatient 登记患者。有植入物时 MaxFieldStrength 必须为正整数。
func (h *Hospital) RegisterPatient(req RegisterPatientRequest) error {
	if !nonemptyID(req.ID) || (!req.NoImplant && req.MaxFieldStrength <= 0) {
		return ErrInvalidArgument
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.patients[req.ID]; exists {
		return ErrInvalidArgument
	}
	h.patients[req.ID] = &patient{
		id:               req.ID,
		highRisk:         req.HighRisk,
		noImplant:        req.NoImplant,
		maxFieldStrength: req.MaxFieldStrength,
		allergic:         req.Allergic,
	}
	return nil
}

// RecordKidney 登记肾功能结果。只认采样时刻最晚者，并列时认登记在后者。
func (h *Hospital) RecordKidney(req RecordKidneyRequest) error {
	if !nonemptyID(req.PatientID) || !validTime(req.SampledAt) || !validTime(req.Now) ||
		req.SampledAt > req.Now {
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
	p, ok := h.patients[req.PatientID]
	if !ok {
		return ErrNotFound
	}
	h.orderSeq++
	cur := kidneyResult{value: req.Value, sampledAt: req.SampledAt, order: h.orderSeq}
	if cur.sampledAt > p.latest.sampledAt ||
		(cur.sampledAt == p.latest.sampledAt && cur.order > p.latest.order) {
		p.latest = cur
	}
	h.commit(req.Now)
	committed = true
	return nil
}
