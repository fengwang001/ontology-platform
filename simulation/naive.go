package simulation

import "ontology/imaging"

type NaiveSystem struct {
	config       imaging.Config
	lastNow      int
	devices      map[string]imaging.Device
	qcs          map[string][]imaging.QualityControl
	exams        map[string]imaging.ExamType
	patients     map[string]imaging.Patient
	results      map[string]imaging.RenalResult
	resultSeq    int64
	appointments map[string]imaging.Appointment
}

func NewNaiveSystem(config imaging.Config) *NaiveSystem {
	return &NaiveSystem{
		config:       config,
		devices:      map[string]imaging.Device{},
		qcs:          map[string][]imaging.QualityControl{},
		exams:        map[string]imaging.ExamType{},
		patients:     map[string]imaging.Patient{},
		results:      map[string]imaging.RenalResult{},
		appointments: map[string]imaging.Appointment{},
	}
}

func (n *NaiveSystem) RegisterDevice(now int, device imaging.Device) error {
	if err := n.gate(now, device.ID != "", validInstant(now), validDevice(device)); err != nil {
		return err
	}
	if _, exists := n.devices[device.ID]; exists {
		return fail(imaging.ErrInvalidState, "device exists")
	}
	n.devices[device.ID] = device
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) RegisterQualityControl(now int, deviceID string, qc imaging.QualityControl) error {
	if err := n.gate(now, deviceID != "", validInstant(now), validQC(qc)); err != nil {
		return err
	}
	if _, exists := n.devices[deviceID]; !exists {
		return fail(imaging.ErrNotFound, "device missing")
	}
	n.qcs[deviceID] = append(n.qcs[deviceID], qc)
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) RegisterExam(now int, exam imaging.ExamType) error {
	if err := n.gate(now, validInstant(now), validExam(exam)); err != nil {
		return err
	}
	if _, exists := n.exams[exam.ID]; exists {
		return fail(imaging.ErrInvalidState, "exam exists")
	}
	n.exams[exam.ID] = exam
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) RegisterPatient(now int, patient imaging.Patient) error {
	if err := n.gate(now, validInstant(now), validPatient(patient)); err != nil {
		return err
	}
	if _, exists := n.patients[patient.ID]; exists {
		return fail(imaging.ErrInvalidState, "patient exists")
	}
	n.patients[patient.ID] = patient
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) RecordRenalResult(now int, patientID string, value, sampledAt int) error {
	if err := n.gate(now, patientID != "", validInstant(now), validInstant(sampledAt), sampledAt <= now); err != nil {
		return err
	}
	if _, exists := n.patients[patientID]; !exists {
		return fail(imaging.ErrNotFound, "patient missing")
	}
	current, exists := n.results[patientID]
	if exists && sampledAt < current.SampledAt {
		n.lastNow = now
		return nil
	}
	n.resultSeq++
	n.results[patientID] = imaging.RenalResult{Value: value, SampledAt: sampledAt, Sequence: n.resultSeq}
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) Book(now int, id, deviceID, patientID, examID string, start int) error {
	if err := n.gate(now, id != "", deviceID != "", patientID != "", examID != "", validInstant(now), validInstant(start)); err != nil {
		return err
	}
	if _, exists := n.appointments[id]; exists {
		return fail(imaging.ErrInvalidState, "appointment exists")
	}
	device, dExists := n.devices[deviceID]
	patient, pExists := n.patients[patientID]
	exam, eExists := n.exams[examID]
	if !dExists || !pExists || !eExists {
		return fail(imaging.ErrNotFound, "reference missing")
	}
	end, occEnd, err := times(start, exam, n.config)
	if err != nil {
		return err
	}
	if err := n.candidate(device, patient, exam, start, end, occEnd, ""); err != nil {
		return err
	}
	n.appointments[id] = imaging.Appointment{ID: id, DeviceID: deviceID, PatientID: patientID, ExamID: examID, Start: start, End: end, OccupancyEnd: occEnd, Status: imaging.StatusAccepted}
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) RecordHydration(now int, id string, startedAt int) error {
	if err := n.gate(now, id != "", validInstant(now), validInstant(startedAt), startedAt <= now); err != nil {
		return err
	}
	appt, exists := n.appointments[id]
	if !exists {
		return fail(imaging.ErrNotFound, "appointment missing")
	}
	if appt.Status != imaging.StatusAccepted {
		return fail(imaging.ErrInvalidState, "not accepted")
	}
	patient, exam := n.patients[appt.PatientID], n.exams[appt.ExamID]
	decision := imaging.RenalDecisionForTest(patient, n.results[appt.PatientID], appt.Start, n.config)
	if !exam.Enhanced || !decision.Fresh || !decision.Sufficient || !decision.NeedHydration {
		return fail(imaging.ErrInvalidState, "hydration not required")
	}
	appt.HasHydration, appt.HydrationAt = true, startedAt
	n.appointments[id] = appt
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) RecordPremedication(now int, id string, startedAt int) error {
	if err := n.gate(now, id != "", validInstant(now), validInstant(startedAt), startedAt <= now); err != nil {
		return err
	}
	appt, exists := n.appointments[id]
	if !exists {
		return fail(imaging.ErrNotFound, "appointment missing")
	}
	if appt.Status != imaging.StatusAccepted {
		return fail(imaging.ErrInvalidState, "not accepted")
	}
	patient, exam := n.patients[appt.PatientID], n.exams[appt.ExamID]
	if !exam.Enhanced || !patient.ContrastAllergy {
		return fail(imaging.ErrInvalidState, "premedication not required")
	}
	appt.HasPremed, appt.PremedAt = true, startedAt
	n.appointments[id] = appt
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) CheckIn(now int, id string) error {
	if err := n.gate(now, id != "", validInstant(now)); err != nil {
		return err
	}
	appt, exists := n.appointments[id]
	if !exists {
		return fail(imaging.ErrNotFound, "appointment missing")
	}
	if appt.Status != imaging.StatusAccepted {
		return fail(imaging.ErrInvalidState, "not accepted")
	}
	if now < appt.Start-30 || now > appt.Start+15 {
		return fail(imaging.ErrInvalidState, "window mismatch")
	}
	patient, exam := n.patients[appt.PatientID], n.exams[appt.ExamID]
	decision := imaging.RenalDecisionForTest(patient, n.results[appt.PatientID], appt.Start, n.config)
	err := check(patient, exam, appt, decision, n.config)
	if err == nil {
		appt.Status = imaging.StatusCheckedIn
		n.appointments[id] = appt
		n.lastNow = now
		return nil
	}
	appt.Status = imaging.StatusReschedule
	appt.HasHydration, appt.HasPremed = false, false
	n.appointments[id] = appt
	n.lastNow = now
	return err
}

func (n *NaiveSystem) Reschedule(now int, id, deviceID string, start int) error {
	if err := n.gate(now, id != "", deviceID != "", validInstant(now), validInstant(start)); err != nil {
		return err
	}
	appt, exists := n.appointments[id]
	if !exists {
		return fail(imaging.ErrNotFound, "appointment missing")
	}
	if appt.Status != imaging.StatusAccepted && appt.Status != imaging.StatusReschedule {
		return fail(imaging.ErrInvalidState, "cannot reschedule")
	}
	device, patient, exam := n.devices[deviceID], n.patients[appt.PatientID], n.exams[appt.ExamID]
	end, occEnd, err := times(start, exam, n.config)
	if err != nil {
		return err
	}
	if err := n.candidate(device, patient, exam, start, end, occEnd, id); err != nil {
		return err
	}
	appt.DeviceID, appt.Start, appt.End, appt.OccupancyEnd = deviceID, start, end, occEnd
	appt.Status = imaging.StatusAccepted
	appt.HasHydration, appt.HasPremed = false, false
	n.appointments[id] = appt
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) Cancel(now int, id string) error {
	if err := n.gate(now, id != "", validInstant(now)); err != nil {
		return err
	}
	appt, exists := n.appointments[id]
	if !exists {
		return fail(imaging.ErrNotFound, "appointment missing")
	}
	if appt.Status == imaging.StatusCheckedIn {
		return fail(imaging.ErrInvalidState, "checked in")
	}
	if appt.Status != imaging.StatusAccepted && appt.Status != imaging.StatusReschedule {
		return fail(imaging.ErrInvalidState, "cannot cancel")
	}
	appt.Status = imaging.StatusCancelled
	n.appointments[id] = appt
	n.lastNow = now
	return nil
}

func (n *NaiveSystem) Appointment(id string) (imaging.Appointment, bool) {
	appt, exists := n.appointments[id]
	return appt, exists
}
