package imaging

import "testing"

func testConfig() Config {
	return Config{
		NormalRenalTTL:      100,
		HighRiskRenalTTL:    50,
		RenalLowLimit:       30,
		RenalHighLimit:      60,
		HydrationLead:       20,
		PremedicationLead:   40,
		ObservationDuration: 30,
		ObservationCapacity: 1,
		CleanupByClass: map[DeviceClass]int{
			DeviceClassCT: 10,
			DeviceClassMR: 5,
		},
	}
}

func setupFixture(t *testing.T, capacity int) *System {
	t.Helper()
	config := testConfig()
	config.ObservationCapacity = capacity
	system := NewSystem(config)
	must(t, system.RegisterDevice(0, Device{ID: "ct", Class: DeviceClassCT}))
	must(t, system.RegisterDevice(0, Device{ID: "mr15", Class: DeviceClassMR, FieldLimit: 15}))
	must(t, system.RegisterDevice(0, Device{ID: "mr30", Class: DeviceClassMR, FieldLimit: 30}))
	must(t, system.RegisterExam(0, ExamType{ID: "ct-enh", DeviceClass: DeviceClassCT, Duration: 20, Enhanced: true}))
	must(t, system.RegisterExam(0, ExamType{ID: "ct-plain", DeviceClass: DeviceClassCT, Duration: 20}))
	must(t, system.RegisterExam(0, ExamType{ID: "mr-plain", DeviceClass: DeviceClassMR, Duration: 20}))
	must(t, system.RegisterExam(0, ExamType{ID: "mr-enh", DeviceClass: DeviceClassMR, Duration: 20, Enhanced: true}))
	must(t, system.RegisterPatient(0, Patient{ID: "p", HighRisk: true, HasImplantLimit: true, ImplantFieldLimit: 15, ContrastAllergy: true}))
	must(t, system.RegisterPatient(0, Patient{ID: "n"}))
	return system
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %s: %s", CodeOf(err), err)
	}
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if got := CodeOf(err); got != want {
		t.Fatalf("error code = %q, want %q (err=%v)", got, want, err)
	}
}

func TestBookingIntervalsTouchButDoNotOverlap(t *testing.T) {
	system := setupFixture(t, 4)
	must(t, system.RecordRenalResult(50, "p", 50, 50))
	must(t, system.Book(51, "a", "ct", "p", "ct-enh", 100))
	must(t, system.Book(52, "b", "ct", "p", "ct-plain", 130))
	assertCode(t, system.Book(53, "c", "ct", "p", "ct-plain", 129), ErrDeviceConflict)
}

func TestQualityControlRepeatsAcrossDays(t *testing.T) {
	system := setupFixture(t, 4)
	must(t, system.RegisterQualityControl(10, "ct", QualityControl{Start: 30, End: 40}))
	assertCode(t, system.Book(11, "a", "ct", "p", "ct-plain", 1440+34), ErrQCConflict)
	must(t, system.Book(12, "b", "ct", "p", "ct-plain", 1440+40))
}

func TestRenalAndFieldBoundaries(t *testing.T) {
	system := setupFixture(t, 4)
	must(t, system.RecordRenalResult(100, "p", 50, 50))
	assertCode(t, system.Book(101, "old", "mr15", "p", "mr-enh", 101), ErrRenalMissing)
	must(t, system.RecordRenalResult(102, "p", 50, 51))
	must(t, system.Book(103, "exact-ttl", "mr15", "p", "mr-enh", 101))

	must(t, system.RecordRenalResult(200, "p", 29, 200))
	assertCode(t, system.Book(201, "low", "mr15", "p", "mr-enh", 220), ErrRenalInsufficient)
	must(t, system.RecordRenalResult(202, "p", 30, 202))
	assertCode(t, system.Book(203, "strong", "mr30", "p", "mr-enh", 220), ErrImplantIncompatible)
	must(t, system.Book(204, "low-boundary", "mr15", "p", "mr-enh", 220))

	must(t, system.RecordRenalResult(300, "p", 60, 300))
	must(t, system.Book(301, "high-boundary", "mr15", "p", "mr-enh", 320))
}

func TestHydrationPremedicationAndCheckInBoundaries(t *testing.T) {
	system := setupFixture(t, 4)
	must(t, system.RecordRenalResult(150, "p", 40, 150))
	must(t, system.Book(151, "a", "mr15", "p", "mr-enh", 200))
	must(t, system.RecordPremedication(160, "a", 160))
	must(t, system.RecordHydration(180, "a", 180))
	must(t, system.CheckIn(190, "a"))

	must(t, system.RecordRenalResult(350, "p", 40, 350))
	must(t, system.Book(351, "b", "mr15", "p", "mr-enh", 400))
	must(t, system.RecordPremedication(360, "b", 360))
	must(t, system.RecordHydration(380, "b", 380))
	assertCode(t, system.CheckIn(416, "b"), ErrInvalidState)
}

func TestFailedCheckInReleasesResources(t *testing.T) {
	system := setupFixture(t, 1)
	must(t, system.RecordRenalResult(200, "p", 40, 200))
	must(t, system.Book(201, "a", "mr15", "p", "mr-plain", 250))
	must(t, system.CheckIn(240, "a"))

	must(t, system.RecordRenalResult(250, "p", 40, 250))
	must(t, system.Book(251, "b", "mr15", "p", "mr-enh", 300))
	assertCode(t, system.CheckIn(295, "b"), ErrInvalidState)
	appt, _ := system.Appointment("b")
	if appt.Status != StatusReschedule {
		t.Fatalf("status = %s, want needs_reschedule", appt.Status)
	}
	must(t, system.Book(300, "c", "mr15", "p", "mr-plain", 325))
}

func TestObservationCapacityExactAndOver(t *testing.T) {
	system := setupFixture(t, 1)
	must(t, system.RecordRenalResult(180, "n", 60, 180))
	must(t, system.Book(181, "a", "mr15", "n", "mr-enh", 200))
	must(t, system.RecordRenalResult(220, "n", 60, 220))
	must(t, system.Book(221, "touch", "ct", "n", "ct-enh", 250))
	assertCode(t, system.Book(222, "overlap", "mr30", "n", "mr-enh", 249), ErrObservationFull)
}

func TestRescheduleCanOverlapItsOldInterval(t *testing.T) {
	system := setupFixture(t, 4)
	must(t, system.RecordRenalResult(190, "p", 60, 190))
	must(t, system.Book(191, "a", "mr15", "p", "mr-enh", 200))
	must(t, system.Reschedule(192, "a", "mr15", 210))
	assertCode(t, system.Book(193, "b", "mr15", "p", "mr-plain", 234), ErrDeviceConflict)
	appt, _ := system.Appointment("a")
	if appt.HasHydration || appt.HasPremed {
		t.Fatal("hydration and premedication must be voided")
	}
}

func TestErrorPriorityAndClockRollback(t *testing.T) {
	system := setupFixture(t, 4)
	assertCode(t, system.Book(0, "", "ct", "p", "mr-plain", 100), ErrInvalidParameter)
	rollback := setupFixture(t, 4)
	must(t, rollback.RecordRenalResult(10, "p", 60, 10))
	assertCode(t, rollback.Book(9, "b", "ct", "p", "ct-plain", 100), ErrClockRollback)
}
