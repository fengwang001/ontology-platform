package battery

const InvalidTemperature = int64(0x5555555555555555)

// Sample is one periodic pack measurement.
type Sample struct {
	TimeMS         int64
	CellVoltagesMV []int64
	Temperatures   []int64 // InvalidTemperature marks an invalid sensor reading
	CurrentMA      int64
}

// validateSample enforces the rejection order:
// count mismatch > value out of range > time not strictly increasing.
// A rejected sample changes no state and does not advance time.
func (c *Config) validateSample(s Sample, lastMS int64, haveLast bool) error {
	if len(s.CellVoltagesMV) != c.CellCount || len(s.Temperatures) != c.TempCount {
		return ErrInvalidSample
	}
	for _, v := range s.CellVoltagesMV {
		if v < c.MinCellVoltageMV || v > c.MaxCellVoltageMV {
			return ErrInvalidSample
		}
	}
	if s.CurrentMA < c.MinCurrentMA || s.CurrentMA > c.MaxCurrentMA {
		return ErrInvalidSample
	}
	// Numeric temperatures outside the temperature domain are treated as an
	// invalid sensor reading, which is legal for a sample; no range error here.
	if s.TimeMS < 0 || (haveLast && s.TimeMS <= lastMS) {
		return ErrTimeNotAdvancing
	}
	return nil
}
