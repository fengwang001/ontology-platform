package battery_test

import (
	"ontology/battery"
)

// testConfig is a small, fully-covered configuration used across the tests.
func testConfig() battery.Config {
	return battery.Config{
		CellCount: 4,
		TempCount: 2,

		ChargeVoltageTable: []battery.LimitTier{
			{Lower: 0, Upper: 3000, LimitPermille: 0},
			{Lower: 3000, Upper: 3600, LimitPermille: 1000},
			{Lower: 3600, Upper: 5000, LimitPermille: 0},
		},
		DischargeVoltageTable: []battery.LimitTier{
			{Lower: 0, Upper: 3000, LimitPermille: 0},
			{Lower: 3000, Upper: 3600, LimitPermille: 1000},
			{Lower: 3600, Upper: 5000, LimitPermille: 1000},
		},
		ChargeTempTable: []battery.LimitTier{
			{Lower: -400, Upper: 0, LimitPermille: 0},
			{Lower: 0, Upper: 450, LimitPermille: 1000},
			{Lower: 450, Upper: 800, LimitPermille: 0},
		},
		DischargeTempTable: []battery.LimitTier{
			{Lower: -400, Upper: 0, LimitPermille: 0},
			{Lower: 0, Upper: 450, LimitPermille: 1000},
			{Lower: 450, Upper: 800, LimitPermille: 0},
		},

		RatedChargeMA:    1000,
		RatedDischargeMA: 800,

		MinCellVoltageMV: 0,
		MaxCellVoltageMV: 5000,
		MinCurrentMA:     -2000,
		MaxCurrentMA:     2000,
		MinTemperature:   -400,
		MaxTemperature:   800,

		OverVoltageMV:       4200,
		UnderVoltageMV:      2800,
		VoltageDeltaLimitMV: 500,
		RecoveryHystMV:      100,

		ConfirmDurationMS:      100,
		RestCurrentThresholdMA: 50,

		CurrentTiers: []battery.CurrentTier{
			{Excess: 0, ToleranceMS: 100},
			{Excess: 200, ToleranceMS: 50},
			{Excess: 500, ToleranceMS: 10},
		},
	}
}

func mkSample(t int64, volts []int64, temps []int64, cur int64) battery.Sample {
	return battery.Sample{TimeMS: t, CellVoltagesMV: volts, Temperatures: temps, CurrentMA: cur}
}

func baseSample(t int64, cur int64) battery.Sample {
	return mkSample(t, []int64{3300, 3300, 3300, 3300}, []int64{250, 250}, cur)
}
