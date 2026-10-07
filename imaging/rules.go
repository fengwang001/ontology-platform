package imaging

const maxTime = 10_000_000
const dayMinutes = 1440

func nonEmptyID(id string) bool {
	return id != ""
}

func validInstant(value int) bool {
	return value >= 0 && value <= maxTime
}

func validDuration(value int) bool {
	return value > 0 && value <= maxTime
}

func validConfig(config Config) bool {
	if !validDuration(config.NormalRenalTTL) ||
		!validDuration(config.HighRiskRenalTTL) ||
		!validDuration(config.HydrationLead) ||
		!validDuration(config.PremedicationLead) ||
		!validDuration(config.ObservationDuration) ||
		config.ObservationCapacity < 0 {
		return false
	}
	return config.CleanupByClass[DeviceClassCT] > 0 && config.CleanupByClass[DeviceClassMR] > 0
}

func validDevice(device Device) bool {
	if !nonEmptyID(device.ID) {
		return false
	}
	if device.Class == DeviceClassCT {
		return device.FieldLimit == 0
	}
	if device.Class == DeviceClassMR {
		return device.FieldLimit > 0
	}
	return false
}

func validQC(qc QualityControl) bool {
	return qc.Start >= 0 && qc.Start < qc.End && qc.End <= dayMinutes
}

func validExam(exam ExamType) bool {
	return nonEmptyID(exam.ID) &&
		(exam.DeviceClass == DeviceClassCT || exam.DeviceClass == DeviceClassMR) &&
		validDuration(exam.Duration)
}

func validPatient(patient Patient) bool {
	if !nonEmptyID(patient.ID) {
		return false
	}
	return !patient.HasImplantLimit || patient.ImplantFieldLimit > 0
}

type RenalDecision struct {
	HasResult     bool
	Fresh         bool
	Sufficient    bool
	NeedHydration bool
}

func EvaluateRenal(patient Patient, result *RenalResult, start int, config Config) RenalDecision {
	if result == nil || result.SampledAt > start {
		return RenalDecision{}
	}
	ttl := config.NormalRenalTTL
	if patient.HighRisk {
		ttl = config.HighRiskRenalTTL
	}
	if start-result.SampledAt > ttl {
		return RenalDecision{HasResult: true}
	}
	if result.Value < config.RenalLowLimit {
		return RenalDecision{HasResult: true, Fresh: true}
	}
	return RenalDecision{
		HasResult:     true,
		Fresh:         true,
		Sufficient:    true,
		NeedHydration: result.Value < config.RenalHighLimit,
	}
}

func RenalDecisionForTest(patient Patient, result RenalResult, start int, config Config) RenalDecision {
	return EvaluateRenal(patient, &result, start, config)
}

func HydrationSatisfied(appt Appointment, decision RenalDecision, config Config) bool {
	return !decision.NeedHydration || (appt.HasHydration && appt.HydrationAt <= appt.Start-config.HydrationLead)
}

func PremedicationSatisfied(patient Patient, exam ExamType, appt Appointment, config Config) bool {
	required := exam.Enhanced && patient.ContrastAllergy
	return !required || (appt.HasPremed && appt.PremedAt <= appt.Start-config.PremedicationLead)
}

func overlapsQualityControl(start, end int, qc QualityControl) bool {
	firstStart := (start / dayMinutes) * dayMinutes
	lastStart := ((end - 1) / dayMinutes) * dayMinutes
	for dayStart := firstStart; dayStart <= lastStart; dayStart += dayMinutes {
		qcStart := dayStart + qc.Start
		qcEnd := dayStart + qc.End
		if start < qcEnd && qcStart < end {
			return true
		}
	}
	return false
}
