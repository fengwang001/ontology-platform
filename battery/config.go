package battery

import "fmt"

// LimitTier defines one left-closed, right-open interval [Lower, Upper).
// LimitPermille is the limit expressed as a permille (0..1000) of the
// direction's rated current.
type LimitTier struct {
	Lower         int64
	Upper         int64 // exclusive; the final tier may use MaxDomain as +Inf
	LimitPermille int32
}

// CurrentTier defines one overcurrent severity band. A sample belongs to the
// highest band whose Excess is strictly exceeded.
type CurrentTier struct {
	Excess      int64 // band lower bound, exclusive
	ToleranceMS int64
}

// Config is the immutable battery pack configuration.
//
// Voltages are in millivolts, temperatures in 0.1 degrees Celsius, currents
// in milliamperes (positive = charging, negative = discharging), times in
// milliseconds.
type Config struct {
	CellCount int
	TempCount int

	ChargeVoltageTable    []LimitTier
	DischargeVoltageTable []LimitTier
	ChargeTempTable       []LimitTier
	DischargeTempTable    []LimitTier

	RatedChargeMA    int64
	RatedDischargeMA int64

	MinCellVoltageMV int64
	MaxCellVoltageMV int64
	MinCurrentMA     int64
	MaxCurrentMA     int64
	MinTemperature   int64
	MaxTemperature   int64

	OverVoltageMV       int64
	UnderVoltageMV      int64
	VoltageDeltaLimitMV int64
	RecoveryHystMV      int64

	ConfirmDurationMS      int64
	RestCurrentThresholdMA int64

	CurrentTiers []CurrentTier
}

func (c *Config) validate() error {
	if c.CellCount < 1 || c.CellCount > 200 {
		return fmt.Errorf("%w: cell count %d out of [1,200]", ErrInvalidConfig, c.CellCount)
	}
	if c.TempCount < 1 || c.TempCount > 16 {
		return fmt.Errorf("%w: temperature point count %d out of [1,16]", ErrInvalidConfig, c.TempCount)
	}
	if !(c.MinCellVoltageMV < c.MaxCellVoltageMV) {
		return fmt.Errorf("%w: voltage domain %d..%d empty", ErrInvalidConfig, c.MinCellVoltageMV, c.MaxCellVoltageMV)
	}
	if !(c.MinTemperature < c.MaxTemperature) {
		return fmt.Errorf("%w: temperature domain %d..%d empty", ErrInvalidConfig, c.MinTemperature, c.MaxTemperature)
	}
	if !(c.MinCurrentMA < 0 && c.MaxCurrentMA > 0) {
		return fmt.Errorf("%w: current domain %d..%d must straddle zero", ErrInvalidConfig, c.MinCurrentMA, c.MaxCurrentMA)
	}
	if c.RatedChargeMA <= 0 || c.RatedDischargeMA <= 0 {
		return fmt.Errorf("%w: rated currents must be positive (charge=%d discharge=%d)", ErrInvalidConfig, c.RatedChargeMA, c.RatedDischargeMA)
	}
	if c.RatedChargeMA > c.MaxCurrentMA || c.RatedDischargeMA > -c.MinCurrentMA {
		return fmt.Errorf("%w: rated current outside the current domain", ErrInvalidConfig)
	}
	if !(c.MinCellVoltageMV <= c.UnderVoltageMV && c.UnderVoltageMV < c.OverVoltageMV && c.OverVoltageMV <= c.MaxCellVoltageMV) {
		return fmt.Errorf("%w: over/under voltage lines inconsistent with the voltage domain (uv=%d ov=%d)", ErrInvalidConfig, c.UnderVoltageMV, c.OverVoltageMV)
	}
	// Release must be possible strictly on the other side of the line and
	// both release thresholds must lie inside the voltage domain.
	if c.RecoveryHystMV < 0 ||
		c.OverVoltageMV-c.RecoveryHystMV < c.MinCellVoltageMV ||
		c.UnderVoltageMV+c.RecoveryHystMV > c.MaxCellVoltageMV {
		return fmt.Errorf("%w: recovery hysteresis %d inconsistent", ErrInvalidConfig, c.RecoveryHystMV)
	}
	if c.VoltageDeltaLimitMV < 0 {
		return fmt.Errorf("%w: voltage delta limit must be non-negative", ErrInvalidConfig)
	}
	if c.ConfirmDurationMS < 0 {
		return fmt.Errorf("%w: confirm duration must be non-negative", ErrInvalidConfig)
	}
	if c.RestCurrentThresholdMA < 0 {
		return fmt.Errorf("%w: rest current threshold must be non-negative", ErrInvalidConfig)
	}
	if c.RestCurrentThresholdMA > c.MaxCurrentMA || c.RestCurrentThresholdMA > -c.MinCurrentMA {
		return fmt.Errorf("%w: rest current threshold outside the current domain", ErrInvalidConfig)
	}

	if err := validateTiers("charge voltage", c.ChargeVoltageTable, c.MinCellVoltageMV, c.MaxCellVoltageMV); err != nil {
		return err
	}
	if err := validateTiers("discharge voltage", c.DischargeVoltageTable, c.MinCellVoltageMV, c.MaxCellVoltageMV); err != nil {
		return err
	}
	if err := validateTiers("charge temperature", c.ChargeTempTable, c.MinTemperature, c.MaxTemperature); err != nil {
		return err
	}
	if err := validateTiers("discharge temperature", c.DischargeTempTable, c.MinTemperature, c.MaxTemperature); err != nil {
		return err
	}

	if len(c.CurrentTiers) != 3 {
		return fmt.Errorf("%w: exactly 3 overcurrent tiers required, got %d", ErrInvalidConfig, len(c.CurrentTiers))
	}
	for i, t := range c.CurrentTiers {
		if t.Excess < 0 || t.ToleranceMS < 0 {
			return fmt.Errorf("%w: overcurrent tier %d has negative excess/tolerance", ErrInvalidConfig, i)
		}
		if i > 0 {
			if t.Excess <= c.CurrentTiers[i-1].Excess {
				return fmt.Errorf("%w: overcurrent tier excess thresholds must be strictly increasing", ErrInvalidConfig)
			}
			if t.ToleranceMS >= c.CurrentTiers[i-1].ToleranceMS {
				return fmt.Errorf("%w: overcurrent tolerances must strictly decrease as excess grows", ErrInvalidConfig)
			}
		}
	}
	return nil
}

// validateTiers checks that tiers are ordered, left-closed/right-open, abut
// each other, and cover [domainLo, domainHi) without gaps or overlaps.
func validateTiers(name string, tiers []LimitTier, domainLo, domainHi int64) error {
	if len(tiers) == 0 {
		return fmt.Errorf("%w: %s table is empty", ErrInvalidConfig, name)
	}
	prev := domainLo
	for i, t := range tiers {
		if t.Lower != prev {
			return fmt.Errorf("%w: %s table tier %d lower=%d does not abut %d (gap/overlap)", ErrInvalidConfig, name, i, t.Lower, prev)
		}
		if t.Upper <= t.Lower {
			return fmt.Errorf("%w: %s table tier %d empty", ErrInvalidConfig, name, i)
		}
		if t.LimitPermille < 0 || t.LimitPermille > 1000 {
			return fmt.Errorf("%w: %s table tier %d permille %d out of [0,1000]", ErrInvalidConfig, name, i, t.LimitPermille)
		}
		prev = t.Upper
	}
	if prev != domainHi {
		return fmt.Errorf("%w: %s table coverage %d..%d does not match domain %d..%d", ErrInvalidConfig, name, tiers[0].Lower, prev, domainLo, domainHi)
	}
	return nil
}

// lookupTier evaluates a left-closed/right-open table. v is assumed to lie in
// the table's domain. The number of tiers is bounded by configuration, so a
// linear scan is both simple and effectively constant-time per sample.
func lookupTier(tiers []LimitTier, v int64) int32 {
	for _, t := range tiers {
		if v < t.Upper {
			return t.LimitPermille
		}
	}
	return tiers[len(tiers)-1].LimitPermille
}
