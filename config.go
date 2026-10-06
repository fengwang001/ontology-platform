package battery

import "errors"

const (
	MinCellCount        = 1
	MaxCellCount        = 200
	MinTemperatureCount = 1
	MaxTemperatureCount = 16
	MinVoltageMV        = int64(0)
	MaxVoltageMV        = int64(5000)
	MinCurrentMA        = int64(-1<<63 + 1)
	MaxCurrentMA        = int64(1<<63 - 1)
)

type LimitTable struct {
	Boundaries []int64
	Permilles  []int
}

type OvercurrentTier struct {
	ExcessMA    int64
	ToleranceMS int64
}

type Config struct {
	CellCount                 int
	TemperatureCount          int
	ChargeVoltageTable        LimitTable
	DischargeVoltageTable     LimitTable
	ChargeTemperatureTable    LimitTable
	DischargeTemperatureTable LimitTable
	RatedChargeCurrentMA      int64
	RatedDischargeCurrentMA   int64
	OvervoltageThresholdMV    int64
	UndervoltageThresholdMV   int64
	DeltaVoltageLimitMV       int64
	RecoveryHysteresisMV      int64
	ConfirmationDurationMS    int64
	IdleCurrentThresholdMA    int64
	OvercurrentTiers          [3]OvercurrentTier
}

func (c Config) validate() error {
	if c.CellCount < MinCellCount || c.CellCount > MaxCellCount {
		return invalidConfig("cell count must be in [1, 200]")
	}
	if c.TemperatureCount < MinTemperatureCount || c.TemperatureCount > MaxTemperatureCount {
		return invalidConfig("temperature count must be in [1, 16]")
	}
	if c.RatedChargeCurrentMA <= 0 || c.RatedChargeCurrentMA > MaxCurrentMA {
		return invalidConfig("rated charge current must be positive")
	}
	if c.RatedDischargeCurrentMA <= 0 || c.RatedDischargeCurrentMA > MaxCurrentMA {
		return invalidConfig("rated discharge current must be positive")
	}
	if err := c.ChargeVoltageTable.validate(MinVoltageMV, true); err != nil {
		return invalidConfig("charge voltage table: " + configErrorMessage(err))
	}
	if err := c.DischargeVoltageTable.validate(MinVoltageMV, true); err != nil {
		return invalidConfig("discharge voltage table: " + configErrorMessage(err))
	}
	if err := c.ChargeTemperatureTable.validate(-1<<63, false); err != nil {
		return invalidConfig("charge temperature table: " + configErrorMessage(err))
	}
	if err := c.DischargeTemperatureTable.validate(-1<<63, false); err != nil {
		return invalidConfig("discharge temperature table: " + configErrorMessage(err))
	}
	if c.OvervoltageThresholdMV <= c.UndervoltageThresholdMV {
		return invalidConfig("overvoltage threshold must be greater than undervoltage threshold")
	}
	if c.OvervoltageThresholdMV < MinVoltageMV || c.OvervoltageThresholdMV > MaxVoltageMV {
		return invalidConfig("overvoltage threshold is outside the voltage domain")
	}
	if c.UndervoltageThresholdMV < MinVoltageMV || c.UndervoltageThresholdMV > MaxVoltageMV {
		return invalidConfig("undervoltage threshold is outside the voltage domain")
	}
	if c.DeltaVoltageLimitMV <= 0 || c.DeltaVoltageLimitMV > MaxVoltageMV-MinVoltageMV {
		return invalidConfig("delta voltage limit must be positive and fit the voltage domain")
	}
	if c.RecoveryHysteresisMV < 0 {
		return invalidConfig("recovery hysteresis must be non-negative")
	}
	if c.OvervoltageThresholdMV-c.RecoveryHysteresisMV < MinVoltageMV ||
		c.UndervoltageThresholdMV+c.RecoveryHysteresisMV > MaxVoltageMV {
		return invalidConfig("recovery thresholds leave the voltage domain")
	}
	if c.ConfirmationDurationMS < 0 {
		return invalidConfig("confirmation duration must be non-negative")
	}
	if c.IdleCurrentThresholdMA < 0 || c.IdleCurrentThresholdMA > MaxCurrentMA {
		return invalidConfig("idle current threshold must be non-negative")
	}
	tiers := c.OvercurrentTiers
	for i := range tiers {
		if tiers[i].ExcessMA <= 0 || tiers[i].ExcessMA > MaxCurrentMA {
			return invalidConfig("overcurrent tier excess must be positive")
		}
		if tiers[i].ToleranceMS < 0 {
			return invalidConfig("overcurrent tolerance must be non-negative")
		}
		if i > 0 {
			if tiers[i].ExcessMA <= tiers[i-1].ExcessMA {
				return invalidConfig("overcurrent tier excesses must be strictly increasing")
			}
			if tiers[i].ToleranceMS >= tiers[i-1].ToleranceMS {
				return invalidConfig("overcurrent tolerances must strictly decrease as excess increases")
			}
		}
	}
	return nil
}

func configErrorMessage(err error) string {
	var batteryError *Error
	if errors.As(err, &batteryError) {
		return batteryError.Message
	}
	return err.Error()
}

func (t LimitTable) validate(firstBoundary int64, boundedVoltage bool) error {
	if len(t.Boundaries) == 0 {
		return invalidConfig("table must contain at least one boundary")
	}
	if len(t.Boundaries) != len(t.Permilles) {
		return invalidConfig("boundary count must equal permille count")
	}
	if t.Boundaries[0] != firstBoundary {
		return invalidConfig("first boundary must cover the start of the domain")
	}
	for i, boundary := range t.Boundaries {
		if t.Permilles[i] < 0 || t.Permilles[i] > 1000 {
			return invalidConfig("permille must be in [0, 1000]")
		}
		if boundedVoltage && (boundary < MinVoltageMV || boundary > MaxVoltageMV) {
			return invalidConfig("boundary is outside the voltage domain")
		}
		if i > 0 && boundary <= t.Boundaries[i-1] {
			return invalidConfig("boundaries must be strictly increasing")
		}
	}
	return nil
}
