package imaging

import "sync"

type System struct {
	mu           sync.Mutex
	config       Config
	validConfig  bool
	lastNow      int
	devices      map[string]Device
	qualityCheck map[string][]QualityControl
	exams        map[string]ExamType
	patients     map[string]Patient
	renalResults map[string]RenalResult
	renalSeq     int64
	appointments map[string]Appointment
	deviceTimes  *intervalTree
	observation  *countTree
}

func NewSystem(config Config) *System {
	return &System{
		config:       config,
		devices:      map[string]Device{},
		qualityCheck: map[string][]QualityControl{},
		exams:        map[string]ExamType{},
		patients:     map[string]Patient{},
		renalResults: map[string]RenalResult{},
		appointments: map[string]Appointment{},
		deviceTimes:  newIntervalTree(),
		observation:  &countTree{},
		validConfig:  validConfig(config),
	}
}

func (s *System) RegisterDevice(now int, device Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(device.ID), validInstant(now), validDevice(device)); err != nil {
		return err
	}
	if _, exists := s.devices[device.ID]; exists {
		return failure(ErrInvalidState, "device already registered")
	}
	s.devices[device.ID] = device
	s.lastNow = now
	return nil
}

func (s *System) RegisterQualityControl(now int, deviceID string, qc QualityControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(deviceID), validInstant(now), validQC(qc)); err != nil {
		return err
	}
	if _, exists := s.devices[deviceID]; !exists {
		return failure(ErrNotFound, "device not found")
	}
	s.qualityCheck[deviceID] = append(s.qualityCheck[deviceID], qc)
	s.lastNow = now
	return nil
}

func (s *System) RegisterExam(now int, exam ExamType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, validInstant(now), validExam(exam)); err != nil {
		return err
	}
	if _, exists := s.exams[exam.ID]; exists {
		return failure(ErrInvalidState, "exam type already registered")
	}
	s.exams[exam.ID] = exam
	s.lastNow = now
	return nil
}

func (s *System) RegisterPatient(now int, patient Patient) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, validInstant(now), validPatient(patient)); err != nil {
		return err
	}
	if _, exists := s.patients[patient.ID]; exists {
		return failure(ErrInvalidState, "patient already registered")
	}
	s.patients[patient.ID] = patient
	s.lastNow = now
	return nil
}

func (s *System) RecordRenalResult(now int, patientID string, value int, sampledAt int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(patientID), validInstant(now), validInstant(sampledAt), sampledAt <= now); err != nil {
		return err
	}
	if _, exists := s.patients[patientID]; !exists {
		return failure(ErrNotFound, "patient not found")
	}
	current, exists := s.renalResults[patientID]
	if !exists || sampledAt >= current.SampledAt {
		s.renalSeq++
		s.renalResults[patientID] = RenalResult{Value: value, SampledAt: sampledAt, Sequence: s.renalSeq}
	}
	s.lastNow = now
	return nil
}

func (s *System) gate(now int, conditions ...bool) error {
	if !s.validConfig {
		return failure(ErrInvalidParameter, "invalid system configuration")
	}
	for _, condition := range conditions {
		if !condition {
			return failure(ErrInvalidParameter, "invalid parameter")
		}
	}
	if now < s.lastNow {
		return failure(ErrClockRollback, "now cannot move backwards")
	}
	return nil
}

func (s *System) Book(now int, appointmentID, deviceID, patientID, examID string, start int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now,
		nonEmptyID(appointmentID), nonEmptyID(deviceID), nonEmptyID(patientID), nonEmptyID(examID),
		validInstant(now), validInstant(start),
	); err != nil {
		return err
	}
	if _, exists := s.appointments[appointmentID]; exists {
		return failure(ErrInvalidState, "appointment already exists")
	}
	device, deviceExists := s.devices[deviceID]
	patient, patientExists := s.patients[patientID]
	exam, examExists := s.exams[examID]
	if !deviceExists || !patientExists || !examExists {
		return failure(ErrNotFound, "device, patient, or exam not found")
	}
	end, occupancyEnd, err := candidateTimes(start, exam, s.config)
	if err != nil {
		return err
	}
	if err := s.validateCandidate(device, patient, exam, start, end, occupancyEnd, ""); err != nil {
		return err
	}
	inserted := s.deviceTimes.insert(deviceID, interval{key: start, end: occupancyEnd, id: appointmentID})
	if exam.Enhanced {
		s.observation.addRange(end, end+s.config.ObservationDuration, 1)
	}
	s.appointments[appointmentID] = Appointment{
		ID: appointmentID, DeviceID: deviceID, PatientID: patientID, ExamID: examID,
		Start: start, End: end, OccupancyEnd: occupancyEnd,
		Status: StatusAccepted, IntervalSeq: inserted.seq, IntervalPriority: inserted.priority,
	}
	s.lastNow = now
	return nil
}

func candidateTimes(start int, exam ExamType, config Config) (int, int, error) {
	end := start + exam.Duration
	occupancyEnd := end + config.CleanupByClass[exam.DeviceClass]
	if occupancyEnd > maxTime {
		return 0, 0, failure(ErrInvalidParameter, "occupancy end beyond time limit")
	}
	return end, occupancyEnd, nil
}

func (s *System) currentRenalResult(patientID string) *RenalResult {
	result, exists := s.renalResults[patientID]
	if !exists {
		return nil
	}
	return &result
}

func (s *System) validateCandidate(device Device, patient Patient, exam ExamType, start, end, occupancyEnd int, excludeID string) error {
	if device.Class != exam.DeviceClass {
		return failure(ErrDeviceClassMismatch, "device class does not match exam")
	}
	if exam.DeviceClass == DeviceClassMR && patient.HasImplantLimit && device.FieldLimit > patient.ImplantFieldLimit {
		return failure(ErrImplantIncompatible, "device field strength exceeds implant limit")
	}
	if exam.Enhanced {
		decision := EvaluateRenal(patient, s.currentRenalResult(patient.ID), start, s.config)
		if !decision.Fresh {
			return failure(ErrRenalMissing, "renal result missing or expired")
		}
		if !decision.Sufficient {
			return failure(ErrRenalInsufficient, "renal result insufficient")
		}
	}
	if len(s.deviceTimes.overlaps(device.ID, start, occupancyEnd, excludeID)) > 0 {
		return failure(ErrDeviceConflict, "device interval overlaps another booking")
	}
	for _, qc := range s.qualityCheck[device.ID] {
		if overlapsQualityControl(start, occupancyEnd, qc) {
			return failure(ErrQCConflict, "device interval overlaps quality control")
		}
	}
	if exam.Enhanced {
		observationEnd := end + s.config.ObservationDuration
		if observationEnd > maxTime || s.observation.maxOnRange(end, observationEnd)+1 > s.config.ObservationCapacity {
			return failure(ErrObservationFull, "observation capacity exceeded")
		}
	}
	return nil
}

func (s *System) releaseResources(appt Appointment) {
	s.deviceTimes.remove(appt.DeviceID, appt.Start, appt.ID, appt.IntervalSeq)
	if s.exams[appt.ExamID].Enhanced {
		s.observation.addRange(appt.End, appt.End+s.config.ObservationDuration, -1)
	}
}

func (s *System) RecordHydration(now int, appointmentID string, startedAt int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(appointmentID), validInstant(now), validInstant(startedAt), startedAt <= now); err != nil {
		return err
	}
	appt, exists := s.appointments[appointmentID]
	if !exists {
		return failure(ErrNotFound, "appointment not found")
	}
	if appt.Status != StatusAccepted {
		return failure(ErrInvalidState, "appointment is not accepted")
	}
	patient, exam := s.patients[appt.PatientID], s.exams[appt.ExamID]
	decision := EvaluateRenal(patient, s.currentRenalResult(appt.PatientID), appt.Start, s.config)
	if !exam.Enhanced || !decision.Fresh || !decision.Sufficient || !decision.NeedHydration {
		return failure(ErrInvalidState, "hydration is not required")
	}
	appt.HasHydration, appt.HydrationAt = true, startedAt
	s.appointments[appointmentID] = appt
	s.lastNow = now
	return nil
}

func (s *System) RecordPremedication(now int, appointmentID string, startedAt int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(appointmentID), validInstant(now), validInstant(startedAt), startedAt <= now); err != nil {
		return err
	}
	appt, exists := s.appointments[appointmentID]
	if !exists {
		return failure(ErrNotFound, "appointment not found")
	}
	if appt.Status != StatusAccepted {
		return failure(ErrInvalidState, "appointment is not accepted")
	}
	patient, exam := s.patients[appt.PatientID], s.exams[appt.ExamID]
	if !exam.Enhanced || !patient.ContrastAllergy {
		return failure(ErrInvalidState, "premedication is not required")
	}
	appt.HasPremed, appt.PremedAt = true, startedAt
	s.appointments[appointmentID] = appt
	s.lastNow = now
	return nil
}

func (s *System) CheckIn(now int, appointmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(appointmentID), validInstant(now)); err != nil {
		return err
	}
	appt, exists := s.appointments[appointmentID]
	if !exists {
		return failure(ErrNotFound, "appointment not found")
	}
	if appt.Status != StatusAccepted {
		return failure(ErrInvalidState, "appointment is not accepted")
	}
	if now < appt.Start-30 || now > appt.Start+15 {
		return failure(ErrInvalidState, "check-in window mismatch")
	}
	patient, exam := s.patients[appt.PatientID], s.exams[appt.ExamID]
	decision := EvaluateRenal(patient, s.currentRenalResult(appt.PatientID), appt.Start, s.config)
	checkErr := checkEnhanced(patient, exam, appt, decision, s.config)
	if checkErr == nil {
		appt.Status = StatusCheckedIn
		s.appointments[appointmentID] = appt
		s.lastNow = now
		return nil
	}
	appt.Status = StatusReschedule
	appt.HasHydration, appt.HasPremed = false, false
	s.appointments[appointmentID] = appt
	s.releaseResources(appt)
	s.lastNow = now
	return checkErr
}

func checkEnhanced(patient Patient, exam ExamType, appt Appointment, decision RenalDecision, config Config) error {
	if !exam.Enhanced {
		return nil
	}
	if !decision.Fresh {
		return failure(ErrRenalMissing, "renal result missing or expired")
	}
	if !decision.Sufficient {
		return failure(ErrRenalInsufficient, "renal result insufficient")
	}
	if !HydrationSatisfied(appt, decision, config) {
		return failure(ErrInvalidState, "hydration missing")
	}
	if !PremedicationSatisfied(patient, exam, appt, config) {
		return failure(ErrInvalidState, "premedication missing")
	}
	return nil
}

func (s *System) Reschedule(now int, appointmentID, deviceID string, start int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(appointmentID), nonEmptyID(deviceID), validInstant(now), validInstant(start)); err != nil {
		return err
	}
	appt, exists := s.appointments[appointmentID]
	if !exists {
		return failure(ErrNotFound, "appointment not found")
	}
	if appt.Status != StatusAccepted && appt.Status != StatusReschedule {
		return failure(ErrInvalidState, "appointment cannot be rescheduled")
	}
	device, patient, exam := s.devices[deviceID], s.patients[appt.PatientID], s.exams[appt.ExamID]
	end, occupancyEnd, err := candidateTimes(start, exam, s.config)
	if err != nil {
		return err
	}
	old := appt
	wasAccepted := old.Status == StatusAccepted
	if wasAccepted {
		s.releaseResources(old)
	}
	if err := s.validateCandidate(device, patient, exam, start, end, occupancyEnd, appointmentID); err != nil {
		if wasAccepted {
			s.deviceTimes.restore(old.DeviceID, interval{key: old.Start, end: old.OccupancyEnd, id: old.ID, seq: old.IntervalSeq, priority: old.IntervalPriority})
			if exam.Enhanced {
				s.observation.addRange(old.End, old.End+s.config.ObservationDuration, 1)
			}
		}
		return err
	}
	inserted := s.deviceTimes.insert(deviceID, interval{key: start, end: occupancyEnd, id: appointmentID})
	if exam.Enhanced {
		s.observation.addRange(end, end+s.config.ObservationDuration, 1)
	}
	appt.DeviceID, appt.Start, appt.End, appt.OccupancyEnd = deviceID, start, end, occupancyEnd
	appt.Status, appt.IntervalSeq, appt.IntervalPriority = StatusAccepted, inserted.seq, inserted.priority
	appt.HasHydration, appt.HasPremed = false, false
	s.appointments[appointmentID] = appt
	s.lastNow = now
	return nil
}

func (s *System) Cancel(now int, appointmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gate(now, nonEmptyID(appointmentID), validInstant(now)); err != nil {
		return err
	}
	appt, exists := s.appointments[appointmentID]
	if !exists {
		return failure(ErrNotFound, "appointment not found")
	}
	if appt.Status == StatusCheckedIn {
		return failure(ErrInvalidState, "checked-in appointment cannot be cancelled")
	}
	if appt.Status != StatusAccepted && appt.Status != StatusReschedule {
		return failure(ErrInvalidState, "appointment cannot be cancelled")
	}
	if appt.Status == StatusAccepted {
		s.releaseResources(appt)
	}
	appt.Status = StatusCancelled
	s.appointments[appointmentID] = appt
	s.lastNow = now
	return nil
}
