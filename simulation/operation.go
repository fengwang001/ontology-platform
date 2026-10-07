package simulation

import "ontology/imaging"

type Operation struct {
	Name      string
	Now       int
	ID        string
	DeviceID  string
	PatientID string
	ExamID    string
	Start     int
	StartedAt int
	Value     int
	SampledAt int
	Device    imaging.Device
	Exam      imaging.ExamType
	Patient   imaging.Patient
	QC        imaging.QualityControl
}

type StepLog struct {
	Index      int
	Input      Operation
	RealError  imaging.ErrorCode
	NaiveError imaging.ErrorCode
	Basis      string
}

func Apply(system *imaging.System, naive *NaiveSystem, op Operation) (error, error) {
	switch op.Name {
	case "register_device":
		return system.RegisterDevice(op.Now, op.Device), naive.RegisterDevice(op.Now, op.Device)
	case "register_qc":
		return system.RegisterQualityControl(op.Now, op.DeviceID, op.QC), naive.RegisterQualityControl(op.Now, op.DeviceID, op.QC)
	case "register_exam":
		return system.RegisterExam(op.Now, op.Exam), naive.RegisterExam(op.Now, op.Exam)
	case "register_patient":
		return system.RegisterPatient(op.Now, op.Patient), naive.RegisterPatient(op.Now, op.Patient)
	case "renal":
		return system.RecordRenalResult(op.Now, op.PatientID, op.Value, op.SampledAt),
			naive.RecordRenalResult(op.Now, op.PatientID, op.Value, op.SampledAt)
	case "book":
		return system.Book(op.Now, op.ID, op.DeviceID, op.PatientID, op.ExamID, op.Start),
			naive.Book(op.Now, op.ID, op.DeviceID, op.PatientID, op.ExamID, op.Start)
	case "hydration":
		return system.RecordHydration(op.Now, op.ID, op.StartedAt), naive.RecordHydration(op.Now, op.ID, op.StartedAt)
	case "premedication":
		return system.RecordPremedication(op.Now, op.ID, op.StartedAt), naive.RecordPremedication(op.Now, op.ID, op.StartedAt)
	case "checkin":
		return system.CheckIn(op.Now, op.ID), naive.CheckIn(op.Now, op.ID)
	case "reschedule":
		return system.Reschedule(op.Now, op.ID, op.DeviceID, op.Start), naive.Reschedule(op.Now, op.ID, op.DeviceID, op.Start)
	case "cancel":
		return system.Cancel(op.Now, op.ID), naive.Cancel(op.Now, op.ID)
	default:
		return fail(imaging.ErrInvalidParameter, "unknown operation"), fail(imaging.ErrInvalidParameter, "unknown operation")
	}
}

type MismatchError struct {
	Real  imaging.ErrorCode
	Naive imaging.ErrorCode
}

func (e *MismatchError) Error() string {
	return "real=" + string(e.Real) + " naive=" + string(e.Naive)
}
