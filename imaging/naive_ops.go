package imaging

import "sort"

func (n *NaiveModel) naiveQC(d *naiveDevice, start, end int) bool {
	firstDay := start / dayMin
	lastDay := (end - 1) / dayMin
	for day := firstDay; day <= lastDay; day++ {
		base := day * dayMin
		lo := start - base
		if lo < 0 {
			lo = 0
		}
		hi := end - base
		if hi > dayMin {
			hi = dayMin
		}
		for _, q := range d.qcs {
			if lo < q.End && q.Start < hi {
				return true
			}
		}
	}
	return false
}

// naiveObservationOK：全量枚举所有留观区间，逐关键时刻扫描。
func (n *NaiveModel) naiveObservationOK(s, e int, skipAptID string) bool {
	type pt struct {
		t, d int
	}
	var pts []pt
	for _, o := range n.obs {
		if o.aptID == skipAptID {
			continue
		}
		pts = append(pts, pt{o.s, 1}, pt{o.e, -1})
	}
	pts = append(pts, pt{s, 1}, pt{e, -1})
	sort.SliceStable(pts, func(i, j int) bool {
		if pts[i].t != pts[j].t {
			return pts[i].t < pts[j].t
		}
		return pts[i].d < pts[j].d
	})
	c := 0
	for _, p := range pts {
		c += p.d
		if c > n.cfg.ObservationCapacity {
			return false
		}
	}
	return true
}

func (n *NaiveModel) naiveKidney(p *patient, start int) error {
	r := p.latest
	if r.order == 0 || r.sampledAt > start {
		return ErrKidneyMissing
	}
	validity := n.cfg.ValidityNormal
	if p.highRisk {
		validity = n.cfg.ValidityHighRisk
	}
	if start-r.sampledAt > validity {
		return ErrKidneyMissing
	}
	if r.value < n.cfg.KidneyLow {
		return ErrKidneyInsufficient
	}
	return nil
}

func (n *NaiveModel) kidneyNeedsWater(p *patient) bool {
	return p.latest.value < n.cfg.KidneyHigh
}

// arrangementOK 独立地判定一次（可能是改约的）安排。
func (n *NaiveModel) arrangementOK(p *patient, ex *examType, d *naiveDevice, deviceID string, start int, skipAptID string) (*naiveApt, error) {
	if ex.class != d.class {
		return nil, ErrDeviceClassMismatch
	}
	if ex.class == ClassMR && !p.noImplant && d.fs > p.maxFieldStrength {
		return nil, ErrImplantIncompatible
	}
	examEnd := start + ex.duration
	end := examEnd + n.cfg.CleaningMinutes[ex.class]
	if end > maxTime {
		return nil, ErrInvalidArgument
	}
	obsEnd := 0
	if ex.enhanced {
		obsEnd = examEnd + n.cfg.ObservationMinutes
		if obsEnd > maxTime {
			return nil, ErrInvalidArgument
		}
		if err := n.naiveKidney(p, start); err != nil {
			return nil, err
		}
	}
	cand := Interval{start, end}
	for _, o := range d.occ {
		if o.aptID == skipAptID {
			continue
		}
		if naiveHalfOpen(o.iv, cand) {
			return nil, ErrDeviceBusy
		}
	}
	if n.naiveQC(d, start, end) {
		return nil, ErrQCConflict
	}
	if ex.enhanced && !n.naiveObservationOK(examEnd, obsEnd, skipAptID) {
		return nil, ErrObservationFull
	}
	return &naiveApt{
		patient: p.id, exam: ex.id, device: deviceID, start: start,
		examEnd: examEnd, end: end, obsEnd: obsEnd, enhanced: ex.enhanced,
	}, nil
}

func (n *NaiveModel) Book(req BookRequest) error {
	if req.ID == "" || req.PatientID == "" || req.ExamTypeID == "" || req.DeviceID == "" ||
		!validTime(req.Start) || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if req.Now < n.lastNow {
		return ErrClockRollback
	}
	if _, dup := n.appts[req.ID]; dup {
		return ErrInvalidArgument
	}
	p, ok := n.patients[req.PatientID]
	if !ok {
		return ErrNotFound
	}
	ex, ok := n.exams[req.ExamTypeID]
	if !ok {
		return ErrNotFound
	}
	d, ok := n.devices[req.DeviceID]
	if !ok {
		return ErrNotFound
	}
	a, err := n.arrangementOK(p, ex, d, req.DeviceID, req.Start, "")
	if err != nil {
		return err
	}
	a.id = req.ID
	a.status = StatusBooked
	d.occ = append(d.occ, naiveOcc{req.ID, Interval{a.start, a.end}})
	if a.enhanced {
		n.obs = append(n.obs, naiveObs{req.ID, a.examEnd, a.obsEnd})
	}
	n.appts[req.ID] = a
	n.lastNow = req.Now
	return nil
}

func (n *NaiveModel) RegisterHydration(req HydrationRequest) error {
	return n.naivePrep(req, true)
}

func (n *NaiveModel) RegisterPremedication(req PremedicationRequest) error {
	return n.naivePrep(req, false)
}

func (n *NaiveModel) naivePrep(req HydrationRequest, hydro bool) error {
	if req.AppointmentID == "" || !validTime(req.StartAt) || !validTime(req.Now) || req.StartAt > req.Now {
		return ErrInvalidArgument
	}
	if req.Now < n.lastNow {
		return ErrClockRollback
	}
	a, ok := n.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status != StatusBooked {
		return ErrInvalidState
	}
	if hydro {
		a.hydro, a.hydroAt = true, req.StartAt
	} else {
		a.prem, a.premAt = true, req.StartAt
	}
	n.lastNow = req.Now
	return nil
}

func (n *NaiveModel) releaseOccupancy(a *naiveApt) {
	d := n.devices[a.device]
	out := d.occ[:0]
	for _, o := range d.occ {
		if o.aptID != a.id {
			out = append(out, o)
		}
	}
	d.occ = out
	if a.enhanced {
		var kept []naiveObs
		for _, o := range n.obs {
			if o.aptID != a.id {
				kept = append(kept, o)
			}
		}
		n.obs = kept
	}
}

func (n *NaiveModel) CheckIn(req CheckInRequest) error {
	if req.AppointmentID == "" || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if req.Now < n.lastNow {
		return ErrClockRollback
	}
	a, ok := n.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status != StatusBooked {
		return ErrInvalidState
	}
	if req.Now < a.start-30 || req.Now > a.start+15 {
		return ErrCheckinWindow
	}
	p := n.patients[a.patient]
	ex := n.exams[a.exam]
	fail := func(err error) error {
		a.status = StatusNeedsReschedule
		n.releaseOccupancy(a)
		n.lastNow = req.Now
		return err
	}
	if ex.enhanced {
		if err := n.naiveKidney(p, a.start); err != nil {
			return fail(err)
		}
		if n.kidneyNeedsWater(p) {
			if !a.hydro || a.hydroAt > a.start-n.cfg.HydrationLead {
				return fail(ErrNotHydrated)
			}
		}
		if p.allergic {
			if !a.prem || a.premAt > a.start-n.cfg.PremedicationLead {
				return fail(ErrNotPremedicated)
			}
		}
	}
	a.status = StatusCheckedIn
	n.lastNow = req.Now
	return nil
}

func (n *NaiveModel) Reschedule(req RescheduleRequest) error {
	if req.AppointmentID == "" || !validTime(req.NewStart) || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if req.Now < n.lastNow {
		return ErrClockRollback
	}
	a, ok := n.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status != StatusBooked && a.status != StatusNeedsReschedule {
		return ErrInvalidState
	}
	target := req.NewDeviceID
	if target == "" {
		target = a.device
	}
	d, ok := n.devices[target]
	if !ok {
		return ErrNotFound
	}
	p := n.patients[a.patient]
	ex := n.exams[a.exam]
	planned, err := n.arrangementOK(p, ex, d, target, req.NewStart, a.id)
	if err != nil {
		return err
	}
	// 通过：摘旧占用。
	n.releaseOccupancy(a)
	a.device = planned.device
	a.start = planned.start
	a.examEnd = planned.examEnd
	a.end = planned.end
	a.obsEnd = planned.obsEnd
	a.status = StatusBooked
	a.hydro, a.hydroAt = false, 0
	a.prem, a.premAt = false, 0
	d.occ = append(d.occ, naiveOcc{a.id, Interval{a.start, a.end}})
	if a.enhanced {
		n.obs = append(n.obs, naiveObs{a.id, a.examEnd, a.obsEnd})
	}
	n.lastNow = req.Now
	return nil
}

func (n *NaiveModel) Cancel(req CancelRequest) error {
	if req.AppointmentID == "" || !validTime(req.Now) {
		return ErrInvalidArgument
	}
	if req.Now < n.lastNow {
		return ErrClockRollback
	}
	a, ok := n.appts[req.AppointmentID]
	if !ok {
		return ErrNotFound
	}
	if a.status == StatusCheckedIn || a.status == StatusCancelled {
		return ErrInvalidState
	}
	if a.status == StatusBooked {
		n.releaseOccupancy(a)
	}
	a.status = StatusCancelled
	n.lastNow = req.Now
	return nil
}
