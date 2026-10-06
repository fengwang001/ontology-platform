package battery_test

import (
	"testing"

	"ontology/battery"
)

func TestOvercurrentCrossesTiers(t *testing.T) {
	// Limit is 1000mA. t=0 excess 100 (tier1, tol 100); t=5 excess 700
	// (tier3, tol 10) mid-run; the run must not latch until t=10, showing
	// both that tier crossing shortens the tolerance and that it does not
	// restart the run.
	m, _ := battery.New(testConfig())
	s, err := m.Submit(baseSample(0, 1100))
	if err != nil {
		t.Fatal(err)
	}
	if s.Latched {
		t.Fatal("tier1 must need 100ms")
	}
	m.Submit(baseSample(5, 1700))
	s, _ = m.Submit(baseSample(9, 1700))
	if s.Latched {
		t.Fatal("cross-tier latch 1ms early")
	}
	s, _ = m.Submit(baseSample(10, 1700))
	if !s.Latched || !hasCause(s, battery.FaultOvercurrent) {
		t.Fatalf("want overcurrent latch via tier3 after crossing tiers, got %+v", s)
	}
	if s.AllowedChargeMA != 0 || s.AllowedDischargeMA != 0 {
		t.Fatal("latch must zero both directions")
	}
}

func TestOvercurrentReturnResetsRun(t *testing.T) {
	m, _ := battery.New(testConfig())
	m.Submit(baseSample(0, 1700))
	m.Submit(baseSample(9, 1700))
	// Back to the allowed value: run start invalidated.
	m.Submit(baseSample(20, 1000))
	m.Submit(baseSample(25, 1700))
	s, _ := m.Submit(baseSample(34, 1700))
	if s.Latched {
		t.Fatalf("new run is only 9ms old, got %+v", s)
	}
	s, _ = m.Submit(baseSample(35, 1700))
	if !s.Latched {
		t.Fatalf("10ms in the new run must latch, got %+v", s)
	}
}

func TestOvercurrentDischargeDirection(t *testing.T) {
	m, _ := battery.New(testConfig())
	// Discharge rated 800; -1700 => excess 900 tier3, tol 10.
	m.Submit(baseSample(0, -1700))
	s, _ := m.Submit(baseSample(10, -1700))
	if !s.Latched || !hasCause(s, battery.FaultOvercurrent) {
		t.Fatalf("want discharge overcurrent latch, got %+v", s)
	}
}

func TestOvercurrentAndDeltaSameSample(t *testing.T) {
	m, _ := battery.New(testConfig())
	// Delta condition and a tier-1 overcurrent run both mature at t=100 in
	// the very same sample; both causes must be listed.
	spread := []int64{3000, 3000, 3000, 3501}
	m.Submit(mkSample(0, spread, []int64{250, 250}, 1100))
	m.Submit(mkSample(90, spread, []int64{250, 250}, 1100))
	s, _ := m.Submit(mkSample(100, spread, []int64{250, 250}, 1100))
	if !s.Latched || !hasCause(s, battery.FaultOvercurrent) || !hasCause(s, battery.FaultVoltageDelta) {
		t.Fatalf("want both causes listed, got %+v", s)
	}
}
