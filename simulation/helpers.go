package simulation

import "ontology/imaging"

const maxTime = 10_000_000
const dayMinutes = 1440

func fail(code imaging.ErrorCode, reason string) error {
	return &imaging.DomainError{Code: code, Reason: reason}
}

func (n *NaiveSystem) gate(now int, conditions ...bool) error {
	for _, condition := range conditions {
		if !condition {
			return fail(imaging.ErrInvalidParameter, "invalid parameter")
		}
	}
	if now < n.lastNow {
		return fail(imaging.ErrClockRollback, "clock rollback")
	}
	return nil
}

func validInstant(value int) bool { return value >= 0 && value <= maxTime }

func validDevice(device imaging.Device) bool {
	if device.ID == "" {
		return false
	}
	if device.Class == imaging.DeviceClassCT {
		return device.FieldLimit == 0
	}
	return device.Class == imaging.DeviceClassMR && device.FieldLimit > 0
}

func validQC(qc imaging.QualityControl) bool {
	return qc.Start >= 0 && qc.Start < qc.End && qc.End <= dayMinutes
}

func validExam(exam imaging.ExamType) bool {
	return exam.ID != "" && (exam.DeviceClass == imaging.DeviceClassCT || exam.DeviceClass == imaging.DeviceClassMR) && exam.Duration > 0
}

func validPatient(patient imaging.Patient) bool {
	return patient.ID != "" && (!patient.HasImplantLimit || patient.ImplantFieldLimit > 0)
}

func times(start int, exam imaging.ExamType, config imaging.Config) (int, int, error) {
	end := start + exam.Duration
	occEnd := end + config.CleanupByClass[exam.DeviceClass]
	if occEnd > maxTime {
		return 0, 0, fail(imaging.ErrInvalidParameter, "past time limit")
	}
	return end, occEnd, nil
}

func check(patient imaging.Patient, exam imaging.ExamType, appt imaging.Appointment, decision imaging.RenalDecision, config imaging.Config) error {
	if !exam.Enhanced {
		return nil
	}
	if !decision.Fresh {
		return fail(imaging.ErrRenalMissing, "missing or expired")
	}
	if !decision.Sufficient {
		return fail(imaging.ErrRenalInsufficient, "insufficient")
	}
	if !imaging.HydrationSatisfied(appt, decision, config) {
		return fail(imaging.ErrInvalidState, "hydration missing")
	}
	if !imaging.PremedicationSatisfied(patient, exam, appt, config) {
		return fail(imaging.ErrInvalidState, "premedication missing")
	}
	return nil
}

func overlapsQC(start, end int, qc imaging.QualityControl) bool {
	first := (start / dayMinutes) * dayMinutes
	last := ((end - 1) / dayMinutes) * dayMinutes
	for day := first; day <= last; day += dayMinutes {
		if start < day+qc.End && day+qc.Start < end {
			return true
		}
	}
	return false
}

func (n *NaiveSystem) candidate(device imaging.Device, patient imaging.Patient, exam imaging.ExamType, start, end, occEnd int, excludeID string) error {
	if device.Class != exam.DeviceClass {
		return fail(imaging.ErrDeviceClassMismatch, "class mismatch")
	}
	if exam.DeviceClass == imaging.DeviceClassMR && patient.HasImplantLimit && device.FieldLimit > patient.ImplantFieldLimit {
		return fail(imaging.ErrImplantIncompatible, "implant mismatch")
	}
	if exam.Enhanced {
		decision := imaging.EvaluateRenal(patient, n.resultPointer(patient.ID), start, n.config)
		if !decision.Fresh {
			return fail(imaging.ErrRenalMissing, "missing or expired")
		}
		if !decision.Sufficient {
			return fail(imaging.ErrRenalInsufficient, "insufficient")
		}
	}
	for otherID, other := range n.appointments {
		if otherID == excludeID || (other.Status != imaging.StatusAccepted && other.Status != imaging.StatusCheckedIn) || other.DeviceID != device.ID {
			continue
		}
		if start < other.OccupancyEnd && other.Start < occEnd {
			return fail(imaging.ErrDeviceConflict, "device conflict")
		}
	}
	for _, qc := range n.qcs[device.ID] {
		if overlapsQC(start, occEnd, qc) {
			return fail(imaging.ErrQCConflict, "qc conflict")
		}
	}
	if exam.Enhanced {
		obsStart, obsEnd := end, end+n.config.ObservationDuration
		if obsEnd > maxTime {
			return fail(imaging.ErrInvalidParameter, "observation past limit")
		}
		count := 1
		for otherID, other := range n.appointments {
			if otherID == excludeID || (other.Status != imaging.StatusAccepted && other.Status != imaging.StatusCheckedIn) || !n.exams[other.ExamID].Enhanced {
				continue
			}
			if obsStart < other.End+n.config.ObservationDuration && other.End < obsEnd {
				count++
			}
		}
		if count > n.config.ObservationCapacity {
			return fail(imaging.ErrObservationFull, "observation full")
		}
	}
	return nil
}

func (n *NaiveSystem) resultPointer(id string) *imaging.RenalResult {
	result, exists := n.results[id]
	if !exists {
		return nil
	}
	return &result
}
