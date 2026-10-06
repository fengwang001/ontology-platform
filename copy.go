package battery

func cloneConfig(cfg Config) Config {
	clone := cfg
	clone.ChargeVoltageTable = cloneLimitTable(cfg.ChargeVoltageTable)
	clone.DischargeVoltageTable = cloneLimitTable(cfg.DischargeVoltageTable)
	clone.ChargeTemperatureTable = cloneLimitTable(cfg.ChargeTemperatureTable)
	clone.DischargeTemperatureTable = cloneLimitTable(cfg.DischargeTemperatureTable)
	clone.OvercurrentTiers = cfg.OvercurrentTiers
	return clone
}

func cloneLimitTable(table LimitTable) LimitTable {
	return LimitTable{
		Boundaries: append([]int64(nil), table.Boundaries...),
		Permilles:  append([]int(nil), table.Permilles...),
	}
}

func cloneSample(sample Sample) Sample {
	return Sample{
		TimeMS:       sample.TimeMS,
		VoltagesMV:   append([]int64(nil), sample.VoltagesMV...),
		Temperatures: append([]TemperatureReading(nil), sample.Temperatures...),
		CurrentMA:    sample.CurrentMA,
	}
}
