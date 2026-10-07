package simulation

import (
	"fmt"
	"math/rand/v2"

	"ontology/imaging"
)

type Scenario struct {
	Config      imaging.Config
	Devices     []string
	CTDevices   []string
	MRDevices   []string
	DeviceClass map[string]imaging.DeviceClass
	Exams       []string
	EnhancedCT  []string
	PlainCT     []string
	EnhancedMR  []string
	PlainMR     []string
	Patients    []string
	Operations  []Operation
}

func GenerateScenario(rng *rand.Rand, steps int) Scenario {
	config := imaging.Config{
		NormalRenalTTL:      40 + rng.IntN(80),
		HighRiskRenalTTL:    20 + rng.IntN(50),
		RenalLowLimit:       30,
		RenalHighLimit:      60,
		HydrationLead:       10 + rng.IntN(30),
		PremedicationLead:   20 + rng.IntN(40),
		ObservationDuration: 20 + rng.IntN(40),
		ObservationCapacity: 1 + rng.IntN(4),
		CleanupByClass: map[imaging.DeviceClass]int{
			imaging.DeviceClassCT: 3 + rng.IntN(8),
			imaging.DeviceClassMR: 3 + rng.IntN(8),
		},
	}
	scenario := Scenario{Config: config, DeviceClass: map[string]imaging.DeviceClass{}}
	now := 0
	add := func(op Operation) {
		op.Now = now
		scenario.Operations = append(scenario.Operations, op)
	}
	deviceCount := 2 + rng.IntN(4)
	for index := range deviceCount {
		class := []imaging.DeviceClass{imaging.DeviceClassCT, imaging.DeviceClassMR}[rng.IntN(2)]
		id := fmt.Sprintf("d%d", index)
		scenario.DeviceClass[id] = class
		device := imaging.Device{ID: id, Class: class}
		if class == imaging.DeviceClassMR {
			device.FieldLimit = 15 * (1 + rng.IntN(3))
			scenario.MRDevices = append(scenario.MRDevices, id)
		}
		if class == imaging.DeviceClassCT {
			scenario.CTDevices = append(scenario.CTDevices, id)
		}
		scenario.Devices = append(scenario.Devices, id)
		add(Operation{Name: "register_device", Device: device})
		now++
		if rng.IntN(2) == 0 {
			start := 100 + rng.IntN(300)
			add(Operation{Name: "register_qc", DeviceID: id, QC: imaging.QualityControl{Start: start, End: start + 30 + rng.IntN(120)}})
			now++
		}
	}
	if len(scenario.CTDevices) == 0 {
		device := imaging.Device{ID: fmt.Sprintf("d%d", deviceCount), Class: imaging.DeviceClassCT}
		scenario.CTDevices = append(scenario.CTDevices, device.ID)
		scenario.Devices = append(scenario.Devices, device.ID)
		scenario.DeviceClass[device.ID] = imaging.DeviceClassCT
		add(Operation{Name: "register_device", Device: device})
		now++
	}
	if len(scenario.MRDevices) == 0 {
		device := imaging.Device{ID: fmt.Sprintf("d%d", deviceCount+1), Class: imaging.DeviceClassMR, FieldLimit: 15}
		scenario.MRDevices = append(scenario.MRDevices, device.ID)
		scenario.Devices = append(scenario.Devices, device.ID)
		scenario.DeviceClass[device.ID] = imaging.DeviceClassMR
		add(Operation{Name: "register_device", Device: device})
		now++
	}
	examKinds := []struct {
		id       string
		class    imaging.DeviceClass
		enhanced bool
	}{
		{"ct-e", imaging.DeviceClassCT, true},
		{"ct-p", imaging.DeviceClassCT, false},
		{"mr-e", imaging.DeviceClassMR, true},
		{"mr-p", imaging.DeviceClassMR, false},
	}
	for _, kind := range examKinds {
		add(Operation{Name: "register_exam", Exam: imaging.ExamType{ID: kind.id, DeviceClass: kind.class, Duration: 10 + rng.IntN(40), Enhanced: kind.enhanced}})
		now++
	}
	for index := range 4 + rng.IntN(5) {
		id := fmt.Sprintf("p%d", index)
		add(Operation{
			Name: "register_patient",
			Patient: imaging.Patient{
				ID:              id,
				HighRisk:        rng.IntN(2) == 0,
				HasImplantLimit: rng.IntN(2) == 0,
				ContrastAllergy: rng.IntN(2) == 0,
			},
		})
		scenario.Operations[len(scenario.Operations)-1].Patient.ImplantFieldLimit = 15 * (1 + rng.IntN(3))
		if !scenario.Operations[len(scenario.Operations)-1].Patient.HasImplantLimit {
			scenario.Operations[len(scenario.Operations)-1].Patient.ImplantFieldLimit = 0
		}
		now++
		scenario.Patients = append(scenario.Patients, id)
	}
	scenario.Exams = []string{"ct-e", "ct-p", "mr-e", "mr-p"}
	scenario.EnhancedCT = []string{"ct-e"}
	scenario.PlainCT = []string{"ct-p"}
	scenario.EnhancedMR = []string{"mr-e"}
	scenario.PlainMR = []string{"mr-p"}
	appointmentExam := map[string]string{}

	for len(scenario.Operations) < steps {
		now += rng.IntN(3)
		patient := scenario.Patients[rng.IntN(len(scenario.Patients))]
		switch rng.IntN(10) {
		case 0, 1:
			sampledAt := max(0, now-rng.IntN(120))
			add(Operation{Name: "renal", PatientID: patient, Value: 20 + rng.IntN(60), SampledAt: sampledAt})
		case 2, 3, 4:
			id := fmt.Sprintf("a%d", len(scenario.Operations))
			examID := scenario.Exams[rng.IntN(len(scenario.Exams))]
			appointmentExam[id] = examID
			deviceID := chooseDevice(scenario, examID, rng)
			add(Operation{Name: "book", ID: id, DeviceID: deviceID, PatientID: patient, ExamID: examID, Start: now + rng.IntN(500)})
		case 5:
			add(Operation{Name: "hydration", ID: randomAppointment(scenario.Operations, rng), StartedAt: now})
		case 6:
			add(Operation{Name: "premedication", ID: randomAppointment(scenario.Operations, rng), StartedAt: now})
		case 7:
			add(Operation{Name: "checkin", ID: randomAppointment(scenario.Operations, rng)})
		case 8:
			appointmentID := randomAppointment(scenario.Operations, rng)
			deviceID := scenario.Devices[rng.IntN(len(scenario.Devices))]
			if examID := appointmentExam[appointmentID]; examID != "" {
				deviceID = chooseDevice(scenario, examID, rng)
			}
			add(Operation{Name: "reschedule", ID: appointmentID, DeviceID: deviceID, Start: now + rng.IntN(500)})
		default:
			add(Operation{Name: "cancel", ID: randomAppointment(scenario.Operations, rng)})
		}
	}
	return scenario
}

func chooseDevice(scenario Scenario, examID string, rng *rand.Rand) string {
	if examID == "ct-e" || examID == "ct-p" {
		return scenario.CTDevices[rng.IntN(len(scenario.CTDevices))]
	}
	if len(scenario.MRDevices) > 0 {
		return scenario.MRDevices[rng.IntN(len(scenario.MRDevices))]
	}
	return scenario.Devices[rng.IntN(len(scenario.Devices))]
}

func randomAppointment(operations []Operation, rng *rand.Rand) string {
	for range 8 {
		op := operations[rng.IntN(len(operations))]
		if op.Name == "book" {
			return op.ID
		}
	}
	return "missing"
}
