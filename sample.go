package battery

type TemperatureReading struct {
	Valid      bool
	ValueDeciC int64
}

type Sample struct {
	TimeMS       int64
	VoltagesMV   []int64
	Temperatures []TemperatureReading
	CurrentMA    int64
}

func (s Sample) validateShape(cfg Config) error {
	if s.TimeMS < 0 {
		return invalidSample("time must be non-negative")
	}
	if len(s.VoltagesMV) != cfg.CellCount || len(s.Temperatures) != cfg.TemperatureCount {
		return invalidSample("sample measurement count does not match configuration")
	}
	for _, voltage := range s.VoltagesMV {
		if voltage < MinVoltageMV || voltage > MaxVoltageMV {
			return invalidSample("voltage is outside the legal voltage domain")
		}
	}
	if s.CurrentMA < MinCurrentMA || s.CurrentMA > MaxCurrentMA {
		return invalidSample("current is outside the legal current domain")
	}
	return nil
}
