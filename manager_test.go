package battery

import (
	"errors"
	"testing"
)

func testConfig() Config {
	voltageTable := LimitTable{
		Boundaries: []int64{0, 3000, 4000},
		Permilles:  []int{200, 500, 1000},
	}
	temperatureTable := LimitTable{
		Boundaries: []int64{-1 << 63, 0, 100},
		Permilles:  []int{100, 500, 1000},
	}
	return Config{
		CellCount:                 3,
		TemperatureCount:          2,
		ChargeVoltageTable:        voltageTable,
		DischargeVoltageTable:     voltageTable,
		ChargeTemperatureTable:    temperatureTable,
		DischargeTemperatureTable: temperatureTable,
		RatedChargeCurrentMA:      1000,
		RatedDischargeCurrentMA:   2000,
		OvervoltageThresholdMV:    4000,
		UndervoltageThresholdMV:   3000,
		DeltaVoltageLimitMV:       50,
		RecoveryHysteresisMV:      10,
		ConfirmationDurationMS:    10,
		IdleCurrentThresholdMA:    100,
		OvercurrentTiers: [3]OvercurrentTier{
			{ExcessMA: 1, ToleranceMS: 20},
			{ExcessMA: 101, ToleranceMS: 10},
			{ExcessMA: 201, ToleranceMS: 0},
		},
	}
}

func sampleAt(timeMS int64, voltages []int64, temperatures []TemperatureReading, currentMA int64) Sample {
	return Sample{
		TimeMS:       timeMS,
		VoltagesMV:   voltages,
		Temperatures: temperatures,
		CurrentMA:    currentMA,
	}
}

func validTemperatures(first, second int64) []TemperatureReading {
	return []TemperatureReading{
		{Valid: true, ValueDeciC: first},
		{Valid: true, ValueDeciC: second},
	}
}

func validVoltages() []int64 {
	return []int64{3500, 3500, 3500}
}

func TestVoltageTableBoundaryAndTemperatureHalfLimit(t *testing.T) {
	manager, err := NewManager(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := manager.Submit(sampleAt(1,
		[]int64{3000, 3500, 3999},
		[]TemperatureReading{{Valid: true, ValueDeciC: 50}, {Valid: false}},
		0))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ChargeCurrentLimitMA != 250 || snapshot.DischargeCurrentLimitMA != 500 {
		t.Fatalf("limits = (%d, %d), want (250, 500)", snapshot.ChargeCurrentLimitMA, snapshot.DischargeCurrentLimitMA)
	}
}

func TestProtectionDurationBoundaryAndInterruption(t *testing.T) {
	manager, _ := NewManager(testConfig())
	voltages := []int64{4000, 3500, 3500}
	temperatures := validTemperatures(50, 50)

	if snapshot, _ := manager.Submit(sampleAt(9, voltages, temperatures, 0)); snapshot.ChargeProhibited {
		t.Fatal("overvoltage must not latch at 9 ms")
	}
	if snapshot, _ := manager.Submit(sampleAt(10, []int64{3999, 3500, 3500}, temperatures, 0)); snapshot.ChargeProhibited {
		t.Fatal("a non-matching sample must reset the run")
	}
	if snapshot, _ := manager.Submit(sampleAt(11, voltages, temperatures, 0)); snapshot.ChargeProhibited {
		t.Fatal("run restarted at 11 ms, so 11 ms cannot satisfy 10 ms")
	}
	snapshot, err := manager.Submit(sampleAt(21, voltages, temperatures, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.ChargeProhibited || snapshot.ChargeCurrentLimitMA != 0 {
		t.Fatal("overvoltage must be active exactly 10 ms after restart")
	}
}

func TestOvercurrentChangesTierDuringRun(t *testing.T) {
	manager, _ := NewManager(testConfig())
	temperatures := validTemperatures(50, 50)
	voltages := validVoltages()

	if _, err := manager.Submit(sampleAt(1, voltages, temperatures, 501)); err != nil {
		t.Fatal(err)
	}
	if snapshot, _ := manager.Submit(sampleAt(9, voltages, temperatures, 501)); snapshot.Latched {
		t.Fatal("low tier at 9 ms must not latch")
	}
	snapshot, _ := manager.Submit(sampleAt(11, voltages, temperatures, 1101))
	if !snapshot.Latched || len(snapshot.LatchReasons) != 1 || snapshot.LatchReasons[0] != FaultOvercurrentCharge {
		t.Fatalf("high tier after 10 ms in the same run must latch: %+v", snapshot)
	}
}

func TestAllTemperaturesInvalidCauseSensorFaultAndZeroLimits(t *testing.T) {
	manager, _ := NewManager(testConfig())
	snapshot, err := manager.Submit(sampleAt(1, validVoltages(),
		[]TemperatureReading{{Valid: false}, {Valid: false}}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.SensorFault || snapshot.ChargeCurrentLimitMA != 0 || snapshot.DischargeCurrentLimitMA != 0 {
		t.Fatalf("invalid sensor snapshot = %+v", snapshot)
	}
}

func TestMultipleLatchReasonsInSameSample(t *testing.T) {
	manager, _ := NewManager(testConfig())
	first := sampleAt(0,
		[]int64{3000, 3500, 3500},
		[]TemperatureReading{{Valid: true, ValueDeciC: 50}, {Valid: true, ValueDeciC: 50}},
		101)
	if _, err := manager.Submit(first); err != nil {
		t.Fatal(err)
	}
	sample := sampleAt(10, first.VoltagesMV, first.Temperatures, 1001)
	snapshot, err := manager.Submit(sample)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Latched {
		t.Fatal("expected latch")
	}
	want := []FaultReason{FaultDeltaVoltage, FaultOvercurrentCharge}
	if len(snapshot.LatchReasons) != len(want) {
		t.Fatalf("reasons = %v, want %v", snapshot.LatchReasons, want)
	}
	for index := range want {
		if snapshot.LatchReasons[index] != want[index] {
			t.Fatalf("reasons = %v, want %v", snapshot.LatchReasons, want)
		}
	}
}

func TestProtectionEvolvesWhileLatchedAndResetOrder(t *testing.T) {
	manager, _ := NewManager(testConfig())
	temperatures := validTemperatures(50, 50)
	voltages := validVoltages()
	_, _ = manager.Submit(sampleAt(0, voltages, temperatures, 1001))

	overvoltage := []int64{4000, 4000, 4000}
	snapshot, _ := manager.Submit(sampleAt(1, overvoltage, temperatures, 0))
	if !snapshot.Latched {
		t.Fatal("expected immediate overcurrent latch")
	}

	assertErrorKind := func(err error, kind ErrorKind) {
		t.Helper()
		var batteryError *Error
		if !errors.As(err, &batteryError) || batteryError.Kind != kind {
			t.Fatalf("error = %v, want %s", err, kind)
		}
	}
	assertErrorKind(manager.Reset(false), ErrorUnauthorized)

	unlatched, _ := NewManager(testConfig())
	assertErrorKind(unlatched.Reset(true), ErrorNotLatched)
	assertErrorKind(manager.Reset(true), ErrorRecoveryNotMet)

	snapshot, _ = manager.Submit(sampleAt(11, overvoltage, temperatures, 0))
	if !snapshot.ChargeProhibited {
		t.Fatal("voltage protection must continue evolving during a latch")
	}
	assertErrorKind(manager.Reset(true), ErrorRecoveryNotMet)

	recovered := []int64{3980, 3980, 3980}
	snapshot, _ = manager.Submit(sampleAt(12, recovered, temperatures, 0))
	snapshot, _ = manager.Submit(sampleAt(22, recovered, temperatures, 0))
	if snapshot.ChargeProhibited {
		t.Fatal("hysteresis recovery should complete after the confirmation duration")
	}

	managerNoSample, _ := NewManager(testConfig())
	_, _ = managerNoSample.Submit(sampleAt(0, validVoltages(), temperatures, 1001))
	assertErrorKind(managerNoSample.Reset(true), ErrorNoSampleAfterLatch)

	if err := manager.Reset(true); err != nil {
		t.Fatalf("reset after valid recovery sample: %v", err)
	}
	if snapshot := manager.Snapshot(); snapshot.Latched || snapshot.ChargeCurrentLimitMA == 0 {
		t.Fatalf("reset did not restore limits: %+v", snapshot)
	}
}

func TestRejectedSamplesLeaveNoTraceAndOrder(t *testing.T) {
	manager, _ := NewManager(testConfig())
	good := sampleAt(5, validVoltages(), validTemperatures(50, 50), 0)
	if _, err := manager.Submit(good); err != nil {
		t.Fatal(err)
	}

	assertErrorKind := func(err error, kind ErrorKind) {
		t.Helper()
		var batteryError *Error
		if !errors.As(err, &batteryError) || batteryError.Kind != kind {
			t.Fatalf("error = %v, want %s", err, kind)
		}
	}

	badCount := good
	badCount.TimeMS = 1
	badCount.VoltagesMV = []int64{1}
	badCount.CurrentMA = 1 << 62
	var err error
	_, err = manager.Submit(badCount)
	assertErrorKind(err, ErrorInvalidSample)

	badValue := good
	badValue.TimeMS = 1
	badValue.VoltagesMV = []int64{5001, 0, 0}
	_, err = manager.Submit(badValue)
	assertErrorKind(err, ErrorInvalidSample)

	old := good
	old.TimeMS = 4
	_, err = manager.Submit(old)
	assertErrorKind(err, ErrorTimeNotAdvancing)

	snapshot := manager.Snapshot()
	if !snapshot.HasSample || snapshot.TimeMS != 5 {
		t.Fatalf("rejected samples changed state: %+v", snapshot)
	}
}

func TestInvalidConfigurationIsRejected(t *testing.T) {
	cfg := testConfig()
	cfg.CellCount = 201
	_, err := NewManager(cfg)
	if err == nil {
		t.Fatal("expected invalid cell count")
	}

	cfg = testConfig()
	cfg.ChargeVoltageTable = LimitTable{
		Boundaries: []int64{100, 3000},
		Permilles:  []int{500, 1000},
	}
	_, err = NewManager(cfg)
	if err == nil {
		t.Fatal("expected voltage table with uncovered domain to be rejected")
	}

	cfg = testConfig()
	cfg.OvercurrentTiers[1].ToleranceMS = cfg.OvercurrentTiers[2].ToleranceMS
	_, err = NewManager(cfg)
	if err == nil {
		t.Fatal("expected non-decreasing overcurrent tolerances to be rejected")
	}
}
