package imaging

import "testing"

func TestOccupancyAdjacencyAndOverlap(t *testing.T) {
	h := newTestHospital()
	seedBasics(t, h)
	// 平扫CT占用 [1000,1050)+清洁30 = [1000,1080)。
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1000, Now: 0}), "第一台")
	// 恰相接：1080 开始合法。
	mustOK(t, h.Book(BookRequest{ID: "b", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1080, Now: 0}), "恰相接")
	// 差一分钟即重叠。
	wantErr(t, h.Book(BookRequest{ID: "c", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1079, Now: 0}), ErrDeviceBusy, "差一分钟冲突")
}

func TestQCDailyRepeat(t *testing.T) {
	h := newTestHospital()
	seedBasics(t, h)
	// 每日 08:00-08:30 质控。平扫占用+清洁=80 分钟。
	mustOK(t, h.AddQC(AddQCRequest{DeviceID: "ct1", Interval: Interval{Start: 480, End: 510}}), "质控")
	// 跨日重复：第二天 08:00 起的预约与质控冲突。
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1440 + 480, Now: 0}), ErrQCConflict, "次日质控冲突")
	// 恰在质控结束时开始：相接合法。
	mustOK(t, h.Book(BookRequest{ID: "b", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1440 + 510, Now: 0}), "质控后相接")
	// 开始较早但清洁后跨越质控起点，也冲突。
	wantErr(t, h.Book(BookRequest{ID: "c", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 1440 + 430, Now: 0}), ErrQCConflict, "清洁跨入质控")
	// 第一日同样生效。
	wantErr(t, h.Book(BookRequest{ID: "d", PatientID: "p", ExamTypeID: "cts", DeviceID: "ct1", Start: 480, Now: 0}), ErrQCConflict, "当日质控冲突")
}

func TestKidneyValidityAndThresholds(t *testing.T) {
	cfg := testConfig()
	cfg.ValidityNormal = 100
	cfg.ValidityHighRisk = 50
	h, err := NewHospital(cfg)
	mustOK(t, err, "配置")
	seedBasics(t, h)
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct2", Class: ClassCT}), "第二台CT")
	mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: "hi", HighRisk: true, NoImplant: true}), "高风险")
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "hi", Value: 60, SampledAt: 0, Now: 0}), "采样")
	// 高风险有效期恰等于 50：start=50 有效。
	mustOK(t, h.Book(BookRequest{ID: "eq", PatientID: "hi", ExamTypeID: "ctp", DeviceID: "ct1", Start: 50, Now: 0}), "有效期恰等")
	// 超一分钟过期。
	wantErr(t, h.Book(BookRequest{ID: "late", PatientID: "hi", ExamTypeID: "ctp", DeviceID: "ct1", Start: 500, Now: 0}), ErrKidneyMissing, "过期")
	// 普通患者 100 有效期。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 60, SampledAt: 0, Now: 0}), "普通采样")
	mustOK(t, h.Book(BookRequest{ID: "n100", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct2", Start: 100, Now: 0}), "普通100有效")
	// 数值恰等于下限 30：受理通过（需水化，签到时核）。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 30, SampledAt: 1950, Now: 1950}), "恰下限")
	mustOK(t, h.Book(BookRequest{ID: "low", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct2", Start: 2000, Now: 1950}), "恰下限受理")
	// 低于下限。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 29, SampledAt: 2950, Now: 2950}), "低于下限")
	wantErr(t, h.Book(BookRequest{ID: "bad", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct2", Start: 3000, Now: 2950}), ErrKidneyInsufficient, "肾功能不足")
	// 恰等于上限 45：无需水化，可直接签到。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 45, SampledAt: 4950, Now: 4950}), "恰上限")
	mustOK(t, h.Book(BookRequest{ID: "hi45", PatientID: "p", ExamTypeID: "ctp", DeviceID: "ct2", Start: 5000, Now: 4950}), "上限受理")
	mustOK(t, h.CheckIn(CheckInRequest{AppointmentID: "hi45", Now: 5000}), "恰上限免水化签到")
}
