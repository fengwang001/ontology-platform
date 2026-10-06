package imaging

func testConfig() Config {
	return Config{
		ValidityNormal:      7 * 1440,
		ValidityHighRisk:    3 * 1440,
		KidneyLow:           30,
		KidneyHigh:          45,
		HydrationLead:       240,
		PremedicationLead:   360,
		ObservationMinutes:  60,
		ObservationCapacity: 2,
		CleaningMinutes: map[DeviceClass]int{
			ClassCT: 30,
			ClassMR: 40,
		},
	}
}

func newTestHospital() *Hospital {
	h, _ := NewHospital(testConfig())
	return h
}

func mustOK(t testingT, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 期望成功，得到 %v", msg, err)
	}
}

func wantErr(t testingT, got error, want error, msg string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: 期望 %v，得到 %v", msg, want, got)
	}
}
