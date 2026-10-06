package imaging

func (f *fuzzState) gen() Op {
	f.step++
	r := f.rng
	// 引导阶段：先把基础对象建起来。
	if len(f.devices) < 4 {
		return f.genDevice()
	}
	if len(f.exams) < 6 {
		return f.genExam()
	}
	if len(f.patients) < 6 {
		return f.genPatient()
	}
	switch r.Intn(12) {
	case 0:
		return f.genDevice()
	case 1:
		return f.genQC()
	case 2:
		return f.genExam()
	case 3:
		return f.genPatient()
	case 4:
		return f.genKidney()
	case 5, 6:
		return f.genBook()
	case 7:
		return f.genHydro()
	case 8:
		return f.genPrem()
	case 9:
		return f.genCheckin()
	case 10:
		return f.genReschedule()
	default:
		return f.genCancel()
	}
}

func (f *fuzzState) genDevice() Op {
	r := f.rng
	id := fmtID("dev", len(f.devices))
	class := DeviceClass(1 + r.Intn(2))
	if len(f.ctDevs) == 0 {
		class = ClassCT
	} else if len(f.mrDevs) == 0 {
		class = ClassMR
	}
	fs := 0
	if class == ClassMR {
		fs = []int{15, 30, 70}[r.Intn(3)]
	}
	f.devices = append(f.devices, id)
	if class == ClassCT {
		f.ctDevs = append(f.ctDevs, id)
	} else {
		f.mrDevs = append(f.mrDevs, id)
	}
	// 少量重复 ID（10%）以触发参数非法。
	if r.Intn(10) == 0 && len(f.devices) > 1 {
		id = f.devices[r.Intn(len(f.devices)-1)]
	}
	return opRegDevice{RegisterDeviceRequest{ID: id, Class: class, FieldStrength: fs}}
}

func (f *fuzzState) genQC() Op {
	r := f.rng
	dev := f.devices[r.Intn(len(f.devices))]
	start := r.Intn(1300)
	end := start + 1 + r.Intn(120)
	if end > 1440 {
		end = 1440
	}
	return opQC{AddQCRequest{DeviceID: dev, Interval: Interval{Start: start, End: end}}}
}

func (f *fuzzState) genExam() Op {
	r := f.rng
	id := fmtID("ex", len(f.exams))
	class := DeviceClass(1 + r.Intn(2))
	dur := 20 + r.Intn(120)
	enh := r.Intn(2) == 0
	f.exams = append(f.exams, examSpec{id, class, dur, enh})
	return opExam{RegisterExamTypeRequest{ID: id, Class: class, Duration: dur, Enhanced: enh}}
}

func (f *fuzzState) genPatient() Op {
	r := f.rng
	id := fmtID("pat", len(f.patients))
	noImp := r.Intn(2) == 0
	maxFS := 0
	if !noImp {
		maxFS = []int{15, 30, 70}[r.Intn(3)]
	}
	spec := patientSpec{id, r.Intn(2) == 0, noImp, maxFS, r.Intn(3) == 0}
	f.patients = append(f.patients, spec)
	return opPatient{RegisterPatientRequest{
		ID: id, HighRisk: spec.highRisk, NoImplant: noImp,
		MaxFieldStrength: maxFS, Allergic: spec.allergic,
	}}
}

func (f *fuzzState) genKidney() Op {
	r := f.rng
	p := f.patients[r.Intn(len(f.patients))]
	now := f.someNow()
	at := now - r.Intn(400)
	if at < 0 {
		at = 0
	}
	value := 10 + r.Intn(80)
	return opKidney{RecordKidneyRequest{PatientID: p.id, Value: value, SampledAt: at, Now: now}}
}

func (f *fuzzState) genBook() Op {
	r := f.rng
	p := f.patients[r.Intn(len(f.patients))]
	ex := f.exams[r.Intn(len(f.exams))]
	dev := f.ctDevs[r.Intn(len(f.ctDevs))]
	if ex.class == ClassMR {
		dev = f.mrDevs[r.Intn(len(f.mrDevs))]
	}
	// 有时故意选错类别设备，触发设备类别不符。
	if r.Intn(5) == 0 {
		if ex.class == ClassCT {
			dev = f.mrDevs[r.Intn(len(f.mrDevs))]
		} else {
			dev = f.ctDevs[r.Intn(len(f.ctDevs))]
		}
	}
	start := 100 + r.Intn(6000)
	now := f.someNowUpTo(start)
	id := fmtID("apt", f.step)
	f.appts = append(f.appts, id)
	return opBook{BookRequest{ID: id, PatientID: p.id, ExamTypeID: ex.id, DeviceID: dev, Start: start, Now: now}}
}

func (f *fuzzState) pickApt() string {
	if len(f.appts) == 0 {
		return "ghost"
	}
	return f.appts[f.rng.Intn(len(f.appts))]
}

func (f *fuzzState) genHydro() Op {
	r := f.rng
	apt := f.pickApt()
	now := f.someNow()
	at := now - r.Intn(300)
	if at < 0 {
		at = 0
	}
	return opHydro{HydrationRequest{AppointmentID: apt, StartAt: at, Now: now}}
}

func (f *fuzzState) genPrem() Op {
	r := f.rng
	apt := f.pickApt()
	now := f.someNow()
	at := now - r.Intn(400)
	if at < 0 {
		at = 0
	}
	return opPrem{PremedicationRequest{AppointmentID: apt, StartAt: at, Now: now}}
}

func (f *fuzzState) genCheckin() Op {
	apt := f.pickApt()
	// 围绕某时刻签到；系统将自行判定窗口。
	return opCheckin{CheckInRequest{AppointmentID: apt, Now: f.someNow()}}
}

func (f *fuzzState) genReschedule() Op {
	r := f.rng
	apt := f.pickApt()
	dev := ""
	if r.Intn(2) == 0 && len(f.ctDevs) > 0 {
		dev = f.devices[r.Intn(len(f.devices))]
	}
	return opResched{RescheduleRequest{AppointmentID: apt, NewStart: 100 + r.Intn(6000), NewDeviceID: dev, Now: f.someNow()}}
}

func (f *fuzzState) genCancel() Op {
	return opCancel{CancelRequest{AppointmentID: f.pickApt(), Now: f.someNow()}}
}

// someNow 返回一个总体非递减但偶发回退的 now（用于触发时钟回退）。
func (f *fuzzState) someNow() int {
	r := f.rng
	if r.Intn(8) == 0 {
		return r.Intn(5000) // 可能回退
	}
	return f.step*30 + r.Intn(200)
}

func (f *fuzzState) someNowUpTo(limit int) int {
	r := f.rng
	if r.Intn(5) == 0 {
		return r.Intn(6000)
	}
	if limit < 1 {
		return 0
	}
	n := limit - r.Intn(500)
	if n < 0 {
		n = 0
	}
	return n
}

func fmtID(prefix string, n int) string {
	return fmtSprintf("%s%d", prefix, n)
}
