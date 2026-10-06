package imaging

import "fmt"

type opQC struct{ req AddQCRequest }

func (o opQC) ApplyMain(h *Hospital) error    { return h.AddQC(o.req) }
func (o opQC) ApplyNaive(n *NaiveModel) error { return n.AddQC(o.req) }
func (o opQC) String() string {
	return fmt.Sprintf("AddQC(dev=%s [%d,%d))", o.req.DeviceID, o.req.Interval.Start, o.req.Interval.End)
}
func (o opQC) Reason() string { return "质控为日内半开区间，与每日重复模板求交" }

type opExam struct{ req RegisterExamTypeRequest }

func (o opExam) ApplyMain(h *Hospital) error    { return h.RegisterExamType(o.req) }
func (o opExam) ApplyNaive(n *NaiveModel) error { return n.RegisterExamType(o.req) }
func (o opExam) String() string {
	return fmt.Sprintf("RegisterExamType(id=%s class=%d dur=%d enh=%v)", o.req.ID, o.req.Class, o.req.Duration, o.req.Enhanced)
}
func (o opExam) Reason() string { return "检查类型确定类别/时长/是否增强" }

type opPatient struct{ req RegisterPatientRequest }

func (o opPatient) ApplyMain(h *Hospital) error    { return h.RegisterPatient(o.req) }
func (o opPatient) ApplyNaive(n *NaiveModel) error { return n.RegisterPatient(o.req) }
func (o opPatient) String() string {
	return fmt.Sprintf("RegisterPatient(id=%s hi=%v noImp=%v maxFS=%d allerg=%v)",
		o.req.ID, o.req.HighRisk, o.req.NoImplant, o.req.MaxFieldStrength, o.req.Allergic)
}
func (o opPatient) Reason() string {
	return "患者确定风险等级、植入物场强上限与过敏史"
}

type opKidney struct{ req RecordKidneyRequest }

func (o opKidney) ApplyMain(h *Hospital) error    { return h.RecordKidney(o.req) }
func (o opKidney) ApplyNaive(n *NaiveModel) error { return n.RecordKidney(o.req) }
func (o opKidney) String() string {
	return fmt.Sprintf("RecordKidney(p=%s v=%d at=%d now=%d)", o.req.PatientID, o.req.Value, o.req.SampledAt, o.req.Now)
}
func (o opKidney) Reason() string {
	return "采样最晚、并列登记后者生效；now 必须单调"
}

type opBook struct{ req BookRequest }

func (o opBook) ApplyMain(h *Hospital) error    { return h.Book(o.req) }
func (o opBook) ApplyNaive(n *NaiveModel) error { return n.Book(o.req) }
func (o opBook) String() string {
	return fmt.Sprintf("Book(id=%s p=%s ex=%s dev=%s start=%d now=%d)",
		o.req.ID, o.req.PatientID, o.req.ExamTypeID, o.req.DeviceID, o.req.Start, o.req.Now)
}
func (o opBook) Reason() string {
	return "优先级：类别>植入物>肾时效>肾下限>设备>质控>留观；半开区间恰相接合法"
}

type opHydro struct{ req HydrationRequest }

func (o opHydro) ApplyMain(h *Hospital) error    { return h.RegisterHydration(o.req) }
func (o opHydro) ApplyNaive(n *NaiveModel) error { return n.RegisterHydration(o.req) }
func (o opHydro) String() string {
	return fmt.Sprintf("Hydration(apt=%s at=%d now=%d)", o.req.AppointmentID, o.req.StartAt, o.req.Now)
}
func (o opHydro) Reason() string { return "仅已受理状态可登记；开始时刻不晚于 now" }

type opPrem struct{ req PremedicationRequest }

func (o opPrem) ApplyMain(h *Hospital) error    { return h.RegisterPremedication(o.req) }
func (o opPrem) ApplyNaive(n *NaiveModel) error { return n.RegisterPremedication(o.req) }
func (o opPrem) String() string {
	return fmt.Sprintf("Premed(apt=%s at=%d now=%d)", o.req.AppointmentID, o.req.StartAt, o.req.Now)
}
func (o opPrem) Reason() string { return "仅已受理状态可登记；开始时刻不晚于 now" }

type opCheckin struct{ req CheckInRequest }

func (o opCheckin) ApplyMain(h *Hospital) error    { return h.CheckIn(o.req) }
func (o opCheckin) ApplyNaive(n *NaiveModel) error { return n.CheckIn(o.req) }
func (o opCheckin) String() string {
	return fmt.Sprintf("CheckIn(apt=%s now=%d)", o.req.AppointmentID, o.req.Now)
}
func (o opCheckin) Reason() string {
	return "窗口[start-30,start+15]；复核肾/水化/预处理，失败转需改期并释放"
}

type opResched struct{ req RescheduleRequest }

func (o opResched) ApplyMain(h *Hospital) error    { return h.Reschedule(o.req) }
func (o opResched) ApplyNaive(n *NaiveModel) error { return n.Reschedule(o.req) }
func (o opResched) String() string {
	return fmt.Sprintf("Reschedule(apt=%s start=%d dev=%q now=%d)",
		o.req.AppointmentID, o.req.NewStart, o.req.NewDeviceID, o.req.Now)
}
func (o opResched) Reason() string {
	return "旧占用不阻挡自身；全条件重判；成功后水化/预处理作废"
}

type opCancel struct{ req CancelRequest }

func (o opCancel) ApplyMain(h *Hospital) error    { return h.Cancel(o.req) }
func (o opCancel) ApplyNaive(n *NaiveModel) error { return n.Cancel(o.req) }
func (o opCancel) String() string {
	return fmt.Sprintf("Cancel(apt=%s now=%d)", o.req.AppointmentID, o.req.Now)
}
func (o opCancel) Reason() string { return "已签到/已取消不可取消；否则释放占用" }
