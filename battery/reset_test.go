package battery_test

import (
	"errors"
	"testing"

	"ontology/battery"
)

func latchDelta(t *testing.T, m *battery.Manager) {
	t.Helper()
	spread := []int64{3000, 3000, 3000, 3501}
	m.Submit(mkSample(0, spread, []int64{250, 250}, 51))
	s, _ := m.Submit(mkSample(100, spread, []int64{250, 250}, 51))
	if !s.Latched {
		t.Fatalf("setup: expected latch, got %+v", s)
	}
}

func TestResetRejectionOrder(t *testing.T) {
	// 1. no permission (even when not latched, permission is checked first).
	m, _ := battery.New(testConfig())
	if _, err := m.Reset(false); !errors.Is(err, battery.ErrNoPermission) {
		t.Fatalf("want ErrNoPermission, got %v", err)
	}
	// 2. permitted but not latched.
	if _, err := m.Reset(true); !errors.Is(err, battery.ErrNotLatched) {
		t.Fatalf("want ErrNotLatched, got %v", err)
	}
	// 3. latched, no accepted sample after the latching sample.
	latchDelta(t, m)
	if _, err := m.Reset(true); !errors.Is(err, battery.ErrNoSampleSinceLatch) {
		t.Fatalf("want ErrNoSampleSinceLatch, got %v", err)
	}
	// 4. sample after latch but recovery conditions not met (spread persists).
	m.Submit(mkSample(200, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 51))
	if _, err := m.Reset(true); !errors.Is(err, battery.ErrRecoveryNotSatisfied) {
		t.Fatalf("want ErrRecoveryNotSatisfied, got %v", err)
	}
	// Now permission denial still takes precedence even while latched.
	if _, err := m.Reset(false); !errors.Is(err, battery.ErrNoPermission) {
		t.Fatalf("permission must be checked first, got %v", err)
	}
}

func TestResetSuccessClearsAndInvalidatesRuns(t *testing.T) {
	m, _ := battery.New(testConfig())
	latchDelta(t, m)

	// A healthy sample after latch: no OV/UV/delta, a valid temperature, rest.
	s, err := m.Submit(mkSample(200, []int64{3300, 3300, 3300, 3300}, []int64{250, 250}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasSampleSinceLatch {
		t.Fatal("a sample while already latched should mark sample-since-latch")
	}
	s, err = m.Reset(true)
	if err != nil {
		t.Fatalf("reset should succeed: %v", err)
	}
	if s.Latched || s.AllowedChargeMA != 1000 || s.AllowedDischargeMA != 800 {
		t.Fatalf("post-reset state wrong: %+v", s)
	}
	// Delta run was invalidated: one borderline sample alone must not relatch.
	s, _ = m.Submit(mkSample(300, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 51))
	if s.Latched {
		t.Fatalf("sustain run must be cleared by reset, got %+v", s)
	}
}

func TestResetRecoveryRequiresValidTempAndRest(t *testing.T) {
	m, _ := battery.New(testConfig())
	latchDelta(t, m)

	m.Submit(mkSample(200, []int64{3300, 3300, 3300, 3300},
		[]int64{battery.InvalidTemperature, battery.InvalidTemperature}, 0))
	if _, err := m.Reset(true); !errors.Is(err, battery.ErrRecoveryNotSatisfied) {
		t.Fatalf("all-invalid temperature must block reset, got %v", err)
	}
	m.Submit(mkSample(300, []int64{3300, 3300, 3300, 3300}, []int64{250, 250}, 51))
	if _, err := m.Reset(true); !errors.Is(err, battery.ErrRecoveryNotSatisfied) {
		t.Fatalf("current above rest threshold must block reset, got %v", err)
	}
}

func TestProtectionEvolvesWhileLatched(t *testing.T) {
	m, _ := battery.New(testConfig())
	latchDelta(t, m)

	// Over-voltage develops after the latch; ban must still engage.
	ov := []int64{4200, 3300, 3300, 3300}
	m.Submit(mkSample(200, ov, []int64{250, 250}, 0))
	s, _ := m.Submit(mkSample(300, ov, []int64{250, 250}, 0))
	if !s.BanCharge {
		t.Fatal("protection state must keep evolving while latched")
	}
	// Recovery sample must also clear the OV condition before reset works.
	m.Submit(mkSample(400, []int64{3300, 3300, 3300, 3300}, []int64{250, 250}, 0))
	// Reset only inspects the most recent sample's instantaneous conditions,
	// even though the OV release state machine has not completed yet.
	s, err := m.Reset(true)
	if err != nil {
		t.Fatalf("healthy last sample should permit reset even before release timer: %v", err)
	}
	if s.Latched {
		t.Fatal("latch should be gone")
	}
	if !s.BanCharge {
		t.Fatal("independent OV ban must survive the latch reset and clear on its own")
	}
}
