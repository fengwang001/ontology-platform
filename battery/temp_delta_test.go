package battery_test

import (
	"testing"

	"ontology/battery"
)

func TestTemperatureInvalidHandling(t *testing.T) {
	t.Run("partial invalid halves floor", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		s, err := m.Submit(mkSample(0, []int64{3300, 3300, 3300, 3300},
			[]int64{250, battery.InvalidTemperature}, 0))
		if err != nil {
			t.Fatal(err)
		}
		if s.SensorFault {
			t.Fatal("partial invalid is not a sensor fault")
		}
		if s.AllowedChargeMA != 500 || s.AllowedDischargeMA != 400 {
			t.Fatalf("want halved limits 500/400, got %d/%d", s.AllowedChargeMA, s.AllowedDischargeMA)
		}
	})

	t.Run("all invalid forces zero and sensor fault", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		s, err := m.Submit(mkSample(0, []int64{3300, 3300, 3300, 3300},
			[]int64{battery.InvalidTemperature, battery.InvalidTemperature}, 0))
		if err != nil {
			t.Fatal(err)
		}
		if !s.SensorFault || s.AllowedChargeMA != 0 || s.AllowedDischargeMA != 0 {
			t.Fatalf("want sensor fault with zero limits, got %+v", s)
		}
	})

	t.Run("out-of-range numeric temperature counts as invalid", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		s, err := m.Submit(mkSample(0, []int64{3300, 3300, 3300, 3300},
			[]int64{250, 99999}, 0))
		if err != nil {
			t.Fatalf("temperature out of domain must be a legal invalid reading, got %v", err)
		}
		if s.AllowedChargeMA != 500 || s.AllowedDischargeMA != 400 {
			t.Fatalf("want halved limits, got %d/%d", s.AllowedChargeMA, s.AllowedDischargeMA)
		}
	})

	t.Run("min and max readings both applied, strictest wins", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		// max 450 is in the right-open zero band for both directions.
		s, _ := m.Submit(mkSample(0, []int64{3300, 3300, 3300, 3300}, []int64{250, 450}, 0))
		if s.AllowedChargeMA != 0 || s.AllowedDischargeMA != 0 {
			t.Fatalf("want zero via 450 boundary band, got %d/%d", s.AllowedChargeMA, s.AllowedDischargeMA)
		}
	})
}

func TestVoltageDeltaBalanceAndLatch(t *testing.T) {
	t.Run("spread at rest requests balance without latching", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		m.Submit(mkSample(0, []int64{3300, 3300, 3300, 3300}, []int64{250, 250}, 0))
		s, _ := m.Submit(mkSample(100, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 0))
		if !s.BalanceRequest {
			t.Fatal("spread > 500 must request balancing")
		}
		if s.Latched {
			t.Fatalf("spread at rest must not latch, got %+v", s)
		}
	})

	t.Run("spread plus charging over threshold latches after confirm", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		s, _ := m.Submit(mkSample(0, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 51))
		if s.Latched {
			t.Fatal("must not latch before confirm duration")
		}
		s, _ = m.Submit(mkSample(99, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 51))
		if s.Latched {
			t.Fatal("1ms early latch is wrong")
		}
		s, _ = m.Submit(mkSample(100, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 51))
		if !s.Latched || !hasCause(s, battery.FaultVoltageDelta) {
			t.Fatalf("want voltage_delta latch, got %+v", s)
		}
	})

	t.Run("exactly rest threshold is not charging", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		s, _ := m.Submit(mkSample(100, []int64{3000, 3000, 3000, 3501}, []int64{250, 250}, 50))
		if s.Latched {
			t.Fatal("current equal to the rest threshold must not count as charging")
		}
	})
}

func hasCause(s battery.Snapshot, c battery.FaultCause) bool {
	for _, got := range s.LatchCauses {
		if got == c {
			return true
		}
	}
	return false
}
