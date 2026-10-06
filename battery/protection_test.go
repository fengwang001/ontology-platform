package battery_test

import (
	"testing"

	"ontology/battery"
)

func TestOverVoltageConfirmExactAndShort(t *testing.T) {
	ov := []int64{4200, 3300, 3300, 3300}
	normal := []int64{3300, 3300, 3300, 3300}

	t.Run("exactly confirm duration", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		if s, _ := m.Submit(mkSample(0, ov, []int64{250, 250}, 0)); s.BanCharge {
			t.Fatal("ban at t=0")
		}
		s, err := m.Submit(mkSample(100, ov, []int64{250, 250}, 0))
		if err != nil {
			t.Fatal(err)
		}
		if !s.BanCharge || s.AllowedChargeMA != 0 {
			t.Fatalf("want charge banned at 100ms, got %+v", s)
		}
	})

	t.Run("one millisecond short", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		m.Submit(mkSample(0, ov, []int64{250, 250}, 0))
		s, _ := m.Submit(mkSample(99, ov, []int64{250, 250}, 0))
		if s.BanCharge {
			t.Fatal("ban must not trigger 1ms early")
		}
	})

	t.Run("one missed sample restarts the run", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		m.Submit(mkSample(0, ov, []int64{250, 250}, 0))
		m.Submit(mkSample(50, ov, []int64{250, 250}, 0))
		m.Submit(mkSample(80, normal, []int64{250, 250}, 0))
		m.Submit(mkSample(100, ov, []int64{250, 250}, 0))
		s, _ := m.Submit(mkSample(170, ov, []int64{250, 250}, 0))
		if s.BanCharge {
			t.Fatalf("run should have restarted at t=100, state=%+v", s)
		}
		s, _ = m.Submit(mkSample(200, ov, []int64{250, 250}, 0))
		if !s.BanCharge {
			t.Fatal("want ban once the restarted run reaches 100ms")
		}
	})

	t.Run("release needs hysteresis and confirm", func(t *testing.T) {
		m, _ := battery.New(testConfig())
		m.Submit(mkSample(0, ov, []int64{250, 250}, 0))
		s, _ := m.Submit(mkSample(100, ov, []int64{250, 250}, 0))
		if !s.BanCharge {
			t.Fatal("setup: expected ban")
		}
		notClear := []int64{4101, 3300, 3300, 3300}
		m.Submit(mkSample(200, notClear, []int64{250, 250}, 0))
		s, _ = m.Submit(mkSample(300, notClear, []int64{250, 250}, 0))
		if !s.BanCharge {
			t.Fatal("ban must remain while not every cell clears by hysteresis")
		}
		clear := []int64{4100, 3300, 3300, 3300}
		m.Submit(mkSample(400, clear, []int64{250, 250}, 0))
		s, _ = m.Submit(mkSample(499, clear, []int64{250, 250}, 0))
		if !s.BanCharge {
			t.Fatal("release 1ms early must not clear")
		}
		s, _ = m.Submit(mkSample(500, clear, []int64{250, 250}, 0))
		if s.BanCharge {
			t.Fatal("want ban released after hysteresis + confirm duration")
		}
	})
}

func TestUnderVoltageDischargeBan(t *testing.T) {
	uv := []int64{2800, 3300, 3300, 3300}
	m, _ := battery.New(testConfig())
	m.Submit(mkSample(0, uv, []int64{250, 250}, 0))
	s, _ := m.Submit(mkSample(100, uv, []int64{250, 250}, 0))
	if !s.BanDischarge || s.AllowedDischargeMA != 0 {
		t.Fatalf("want discharge banned, got %+v", s)
	}
	clear := []int64{2901, 3300, 3300, 3300}
	m.Submit(mkSample(200, clear, []int64{250, 250}, 0))
	s, _ = m.Submit(mkSample(300, clear, []int64{250, 250}, 0))
	if s.BanDischarge {
		t.Fatalf("want discharge ban released, got %+v", s)
	}
}
