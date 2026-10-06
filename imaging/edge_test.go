package imaging

import "testing"

func seedBasics(t *testing.T, h *Hospital) {
	t.Helper()
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct1", Class: ClassCT}), "登记CT")
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "mr15", Class: ClassMR, FieldStrength: 15}), "登记MR1.5")
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "mr30", Class: ClassMR, FieldStrength: 30}), "登记MR3.0")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "ctp", Class: ClassCT, Duration: 100, Enhanced: true}), "登记增强CT")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "cts", Class: ClassCT, Duration: 50, Enhanced: false}), "登记平扫CT")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "mre", Class: ClassMR, Duration: 120, Enhanced: true}), "登记增强MR")
	mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: "p", NoImplant: true}), "普通无植入")
}

func TestInvalidArgumentAndClock(t *testing.T) {
	h := newTestHospital()
	wantErr(t, h.RegisterDevice(RegisterDeviceRequest{ID: "", Class: ClassCT}), ErrInvalidArgument, "空ID")
	wantErr(t, h.RegisterDevice(RegisterDeviceRequest{ID: "mr", Class: ClassMR}), ErrInvalidArgument, "MR无场强")
	mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: "p", NoImplant: true}), "登记患者")
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 50, SampledAt: 100, Now: 100}), "肾功能100")
	wantErr(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 50, SampledAt: 90, Now: 80}), ErrInvalidArgument, "采样晚于now")
	wantErr(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 50, SampledAt: 90, Now: 99}), ErrClockRollback, "时钟回退")
	// 被拒绝操作不改变时钟：now=101 仍应被接受。
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 51, SampledAt: 101, Now: 101}), "回退后可前进")
}

func TestObjectNotFoundPriority(t *testing.T) {
	h := newTestHospital()
	seedBasics(t, h)
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "p", Value: 50, SampledAt: 1000, Now: 1000}), "肾功能")
	// 患者不存在优先于设备不存在等（对象不存在先于类别判定）。
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "ghost", ExamTypeID: "mre", DeviceID: "ct1", Start: 2000, Now: 1000}), ErrNotFound, "患者不存在")
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "ghost", DeviceID: "ct1", Start: 2000, Now: 1000}), ErrNotFound, "检查类型不存在")
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "mre", DeviceID: "ghost", Start: 2000, Now: 1000}), ErrNotFound, "设备不存在")
	wantErr(t, h.Book(BookRequest{ID: "a", PatientID: "p", ExamTypeID: "mre", DeviceID: "ct1", Start: 2000, Now: 1000}), ErrDeviceClassMismatch, "类别不符先于肾")
}

func TestImplantFieldStrengthEquality(t *testing.T) {
	h := newTestHospital()
	seedBasics(t, h)
	mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: "imp", MaxFieldStrength: 15}), "植入上限15")
	mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: "imp", Value: 60, SampledAt: 1000, Now: 1000}), "肾功能")
	// 场强恰等于允许值：兼容。
	mustOK(t, h.Book(BookRequest{ID: "a", PatientID: "imp", ExamTypeID: "mre", DeviceID: "mr15", Start: 2000, Now: 1000}), "恰等于场强")
	wantErr(t, h.Book(BookRequest{ID: "b", PatientID: "imp", ExamTypeID: "mre", DeviceID: "mr30", Start: 3000, Now: 1000}), ErrImplantIncompatible, "超场强")
}
