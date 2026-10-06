package battery_test

import (
	"errors"
	"testing"

	"ontology/battery"
)

func TestConfigValidation(t *testing.T) {
	if _, err := battery.New(testConfig()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*battery.Config)
	}{
		{"cell count zero", func(c *battery.Config) { c.CellCount = 0 }},
		{"cell count too high", func(c *battery.Config) { c.CellCount = 201 }},
		{"temp count too high", func(c *battery.Config) { c.TempCount = 17 }},
		{"rated non-positive", func(c *battery.Config) { c.RatedChargeMA = 0 }},
		{"voltage lines inverted", func(c *battery.Config) { c.OverVoltageMV, c.UnderVoltageMV = c.UnderVoltageMV, c.OverVoltageMV }},
		{"table gap", func(c *battery.Config) {
			c.ChargeVoltageTable = []battery.LimitTier{
				{Lower: 0, Upper: 3000, LimitPermille: 500},
				{Lower: 3100, Upper: 3600, LimitPermille: 500},
				{Lower: 3600, Upper: 5000, LimitPermille: 500},
			}
		}},
		{"table overlap", func(c *battery.Config) {
			c.ChargeVoltageTable = []battery.LimitTier{
				{Lower: 0, Upper: 3000, LimitPermille: 500},
				{Lower: 2900, Upper: 3600, LimitPermille: 500},
				{Lower: 3600, Upper: 5000, LimitPermille: 500},
			}
		}},
		{"table not covering domain end", func(c *battery.Config) {
			c.ChargeVoltageTable = []battery.LimitTier{
				{Lower: 0, Upper: 3000, LimitPermille: 500},
				{Lower: 3000, Upper: 4900, LimitPermille: 500},
			}
		}},
		{"table bad domain start", func(c *battery.Config) {
			c.ChargeVoltageTable = []battery.LimitTier{
				{Lower: 100, Upper: 3000, LimitPermille: 500},
				{Lower: 3000, Upper: 5000, LimitPermille: 500},
			}
		}},
		{"permille out of range", func(c *battery.Config) { c.ChargeVoltageTable[1].LimitPermille = 1001 }},
		{"wrong tier count", func(c *battery.Config) { c.CurrentTiers = c.CurrentTiers[:2] }},
		{"tolerances not decreasing", func(c *battery.Config) { c.CurrentTiers[1].ToleranceMS = 100 }},
		{"excess not increasing", func(c *battery.Config) { c.CurrentTiers[1].Excess = 0 }},
		{"negative confirm duration", func(c *battery.Config) { c.ConfirmDurationMS = -1 }},
		{"hyst makes release leave domain", func(c *battery.Config) { c.RecoveryHystMV = 5000 }},
		{"empty table", func(c *battery.Config) { c.ChargeTempTable = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mut(&cfg)
			_, err := battery.New(cfg)
			if !errors.Is(err, battery.ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestTierBoundaryEquality(t *testing.T) {
	m, err := battery.New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		v             int64
		wantCharge    int64
		wantDischarge int64
	}{
		{2999, 0, 0},
		{3000, 1000, 800},
		{3599, 1000, 800},
		{3600, 0, 800},
	} {
		// Charge uses the max cell, discharge the min; keep all equal to hit
		// the same boundary in both tables.
		s := mkSample(tc.v, []int64{tc.v, tc.v, tc.v, tc.v}, []int64{250, 250}, 0)
		got, err := m.Submit(s)
		if err != nil {
			t.Fatalf("v=%d: %v", tc.v, err)
		}
		if got.AllowedChargeMA != tc.wantCharge || got.AllowedDischargeMA != tc.wantDischarge {
			t.Fatalf("v=%d: got chg/dis=%d/%d want %d/%d", tc.v, got.AllowedChargeMA, got.AllowedDischargeMA, tc.wantCharge, tc.wantDischarge)
		}
	}
}
