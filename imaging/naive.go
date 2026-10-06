package imaging

// NaiveModel 是独立编写的朴素参考实现：
// 设备占用、留观事件全部用切片保存，每次判定全量线性扫描；
// 不复用主实现的 treap 与判定函数，作为差分对照的独立 oracle。
type NaiveModel struct {
	cfg      Config
	lastNow  int
	devices  map[string]*naiveDevice
	exams    map[string]*examType
	patients map[string]*patient
	appts    map[string]*naiveApt
	obs      []naiveObs // 逐条留观区间
	order    int
}

type naiveDevice struct {
	class DeviceClass
	fs    int
	qcs   []Interval
	occ   []naiveOcc
}

type naiveOcc struct {
	aptID string
	iv    Interval
}

type naiveObs struct {
	aptID string
	s, e  int
}

type naiveApt struct {
	id                    string
	patient, exam, device string
	start, examEnd, end   int
	obsEnd                int
	status                AppointmentStatus
	hydroAt               int
	hydro                 bool
	premAt                int
	prem                  bool
	enhanced              bool
}

func NewNaiveModel(cfg Config) *NaiveModel {
	return &NaiveModel{
		cfg:      cfg,
		devices:  map[string]*naiveDevice{},
		exams:    map[string]*examType{},
		patients: map[string]*patient{},
		appts:    map[string]*naiveApt{},
	}
}

func naiveHalfOpen(a, b Interval) bool { return a.Start < b.End && b.Start < a.End }

func (n *NaiveModel) RegisterDevice(req RegisterDeviceRequest) error {
	if req.ID == "" || (req.Class != ClassCT && req.Class != ClassMR) {
		return ErrInvalidArgument
	}
	if req.Class == ClassMR && req.FieldStrength <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := n.devices[req.ID]; ok {
		return ErrInvalidArgument
	}
	n.devices[req.ID] = &naiveDevice{class: req.Class, fs: req.FieldStrength}
	return nil
}

func (n *NaiveModel) AddQC(req AddQCRequest) error {
	if req.DeviceID == "" || req.Interval.Start < 0 || req.Interval.End <= req.Interval.Start ||
		req.Interval.End > dayMin {
		return ErrInvalidArgument
	}
	d, ok := n.devices[req.DeviceID]
	if !ok {
		return ErrNotFound
	}
	d.qcs = append(d.qcs, req.Interval)
	return nil
}

func (n *NaiveModel) RegisterExamType(req RegisterExamTypeRequest) error {
	if req.ID == "" || req.Duration <= 0 || (req.Class != ClassCT && req.Class != ClassMR) {
		return ErrInvalidArgument
	}
	if _, ok := n.exams[req.ID]; ok {
		return ErrInvalidArgument
	}
	n.exams[req.ID] = &examType{id: req.ID, class: req.Class, duration: req.Duration, enhanced: req.Enhanced}
	return nil
}

func (n *NaiveModel) RegisterPatient(req RegisterPatientRequest) error {
	if req.ID == "" || (!req.NoImplant && req.MaxFieldStrength <= 0) {
		return ErrInvalidArgument
	}
	if _, ok := n.patients[req.ID]; ok {
		return ErrInvalidArgument
	}
	n.patients[req.ID] = &patient{
		id: req.ID, highRisk: req.HighRisk, noImplant: req.NoImplant,
		maxFieldStrength: req.MaxFieldStrength, allergic: req.Allergic,
	}
	return nil
}

func (n *NaiveModel) RecordKidney(req RecordKidneyRequest) error {
	if req.PatientID == "" || !validTime(req.SampledAt) || !validTime(req.Now) ||
		req.SampledAt > req.Now {
		return ErrInvalidArgument
	}
	if req.Now < n.lastNow {
		return ErrClockRollback
	}
	p, ok := n.patients[req.PatientID]
	if !ok {
		return ErrNotFound
	}
	n.order++
	cand := kidneyResult{value: req.Value, sampledAt: req.SampledAt, order: n.order}
	if cand.sampledAt > p.latest.sampledAt ||
		(cand.sampledAt == p.latest.sampledAt && cand.order > p.latest.order) {
		p.latest = cand
	}
	n.lastNow = req.Now
	return nil
}
