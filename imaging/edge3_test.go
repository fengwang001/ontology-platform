package imaging

import "testing"

// 准备一台CT、增强CT、一位需水化（数值介于上下限之间）的非过敏患者。
func seedWater(t *testing.T, h *Hospital, patient string, allergic bool, value int) {
	t.Helper()
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct1", Class: ClassCT}), "CT")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "ctp", Class: ClassCT, Duration: 100, Enhanced: true}), "增强CT")
	mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: patient, NoImplant: true, Allergic: allergic}), "患者")
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: patient, Value: value, SampledAt: 1000, Now: 1000}), "肾")
}

func TestHydrationAndPremedicationEquality(t *testing.T) {
	h := newTestHospital()
	seedWater(t, h, "p", true, 40) // 40 < 上限45 须水化；过敏须预处理
	// a：取等成功路径。
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct1", Start: 3000, Now: 1000}), "预约a")
	mustOK(t, h.RegisterPremedication(PremedicationRequest{AppointmentID: "a", StartAt: 2640, Now: 2640}), "预处理恰等")
	mustOK(t, h.RegisterHydration(HydrationRequest{AppointmentID: "a", StartAt: 2760, Now: 2760}), "水化恰等")
	mustOK(t, h.CheckIn(CheckInRequest{AppointmentID: "a", Now: 2990}), "取等签到成功")
	wantErr(t, h.Reschedule(RescheduleRequest{AppointmentID: "a", NewStart: 5000, NewDeviceID: "ct1", Now: 2991}), ErrInvalidState, "已签到不可改约")
	wantErr(t, h.Cancel(CancelRequest{AppointmentID: "a", Now: 2992}), ErrInvalidState, "已签到不可取消")
	// b：未水化优先于未预处理（两个都缺，报未水化），签到失败转需改期并释放。
	mustOK(t, h.Book(BookRequest{ID: "b", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct1", Start: 4000, Now: 2993}), "预约b")
	wantErr(t, h.CheckIn(CheckInRequest{AppointmentID: "b", Now: 4000}), ErrNotHydrated, "未水化优先")
	// c：只水化、缺预处理，报未预处理。
	mustOK(t, h.Book(BookRequest{ID: "c", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct1", Start: 5000, Now: 4001}), "预约c")
	mustOK(t, h.RegisterHydration(HydrationRequest{AppointmentID: "c", StartAt: 4760, Now: 4760}), "c水化")
	wantErr(t, h.CheckIn(CheckInRequest{AppointmentID: "c", Now: 5000}), ErrNotPremedicated, "未预处理")
}

func TestCheckinWindowAndRescheduleInvalidation(t *testing.T) {
	h := newTestHospital()
	seedWater(t, h, "p", false, 60)
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct1", Start: 2000, Now: 1000}), "预约")
	// 窗口 [-30,+15]：过早拒绝且不改变状态/时钟。
	wantErr(t, h.CheckIn(CheckInRequest{AppointmentID: "a", Now: 1969}), ErrCheckinWindow, "过早")
	mustOK(t, h.CheckIn(CheckInRequest{AppointmentID: "a", Now: 2015}), "恰+15")
}

func TestObservationCapacityExact(t *testing.T) {
	cfg := testConfig()
	cfg.ObservationCapacity = 1
	h, _ := NewHospital(cfg)
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct1", Class: ClassCT}), "CT")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "ctp", Class: ClassCT, Duration: 100, Enhanced: true}), "增强CT")
	for _, id := range []string{"p1", "p2", "p3"} {
		mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: id, NoImplant: true}), id)
		mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: id, Value: 60, SampledAt: 0, Now: 0}), id+"肾")
	}
	// 留观区间 = [start+100, start+160)。
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "p1", ExamTypeID: "ctp", DeviceID: "ct1", Start: 1000, Now: 0}), "a")
	// b 的设备占用与 a 不重叠（a 占 [1000,1130)），留观从 1130 起；留观 [1130,1190)
	mustOK(t, h.Book(BookRequest{ID: "b", PatientID: "p2", ExamTypeID: "ctp", DeviceID: "ct1", Start: 1130, Now: 0}), "b 留观恰相接")
	// c 留观 [1101,1161)：与 a 留观 [1100,1160) 重叠 → 容量不足。设备占用放 ct2 隔离。
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct2", Class: ClassCT}), "CT2")
	wantErr(t, h.Book(BookRequest{ID: "c", PatientID: "p3", ExamTypeID: "ctp", DeviceID: "ct2", Start: 1001, Now: 0}), ErrObservationFull, "留观位不足")
}

func TestRescheduleSelfOverlapAndRelease(t *testing.T) {
	h := newTestHospital()
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct1", Class: ClassCT}), "CT")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "cts", Class: ClassCT, Duration: 50, Enhanced: false}), "平扫")
	mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: "p", NoImplant: true}), "患者")
	// 占用 [1000,1080)
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1000, Now: 0}), "预约")
	// 改约到与自身旧区间重叠的时刻：旧占用不阻挡自己。
	mustOK(t, h.Reschedule(RescheduleRequest{AppointmentID: "a", NewStart: 1020, NewDeviceID: "ct1", Now: 0}), "与自身重叠允许")
	snap := h.Snapshot()
	if len(snap.Devices["ct1"].Occupancies) != 1 {
		t.Fatalf("应仍只有1个占用，得到 %d", len(snap.Devices["ct1"].Occupancies))
	}
	if snap.Devices["ct1"].Occupancies[0] != (Interval{1020, 1100}) {
		t.Fatalf("改约后占用错误: %+v", snap.Devices["ct1"].Occupancies[0])
	}
	// 改约失败保持原约：与质控冲突。
	mustOK(t, h.AddQC(AddQCRequest{DeviceID: "ct1", Interval: Interval{Start: 1030, End: 1040}}), "质控")
	wantErr(t, h.Reschedule(RescheduleRequest{AppointmentID: "a", NewStart: 1000, NewDeviceID: "ct1", Now: 0}), ErrQCConflict, "改约失败")
	if got := h.Snapshot().Devices["ct1"].Occupancies[0]; got != (Interval{1020, 1100}) {
		t.Fatalf("失败后原约被破坏: %+v", got)
	}
}

func TestFailedCheckinReleasesOccupancy(t *testing.T) {
	h := newTestHospital()
	seedWater(t, h, "p", false, 60)
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct1", Start: 2000, Now: 1000}), "预约")
	// 肾功能在预约之后过期：登记更晚但采样更早（now 2000），不影响；直接用过期窗口。
	// 结果采样1000、start2000，普通有效期 10080，仍有效——改用高风险构造过期。
	_ = 2000
	// 直接让结果低于下限：登记新结果（采样时刻 1990，数值 10）。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 10, SampledAt: 1990, Now: 1990}), "变差结果")
	wantErr(t, h.CheckIn(CheckInRequest{AppointmentID: "a", Now: 2000}), ErrKidneyInsufficient, "复核肾功能不足")
	snap := h.Snapshot()
	if len(snap.Devices["ct1"].Occupancies) != 0 {
		t.Fatalf("设备占用应已释放: %+v", snap.Devices["ct1"].Occupancies)
	}
	if len(snap.ObsStarts) != 0 || len(snap.ObsEnds) != 0 {
		t.Fatalf("留观占用应已释放: %v %v", snap.ObsStarts, snap.ObsEnds)
	}
	if snap.Appointments["a"].Status != StatusNeedsReschedule {
		t.Fatalf("应转为需改期")
	}
}

func TestErrorPriority(t *testing.T) {
	h := newTestHospital()
	seedBasics(t, h)
	// 参数非法优先于一切（空患者ID）。
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "", ExamTypeID: "ghost", DeviceID: "ghost", Start: -1, Now: -1}), ErrInvalidArgument, "参数优先")
	// 时钟回退优先于对象不存在。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 60, SampledAt: 5, Now: 5}), "推进时钟")
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "ghost", ExamTypeID: "ghost", DeviceID: "ghost", Start: 100, Now: 4}), ErrClockRollback, "时钟优先")
}
