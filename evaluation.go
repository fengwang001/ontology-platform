package battery

type currentLimits struct {
	charge    int64
	discharge int64
}

func calculateLimits(cfg Config, sample Sample, minVoltage, maxVoltage int64) currentLimits {
	validTemperatures := make([]int64, 0, len(sample.Temperatures))
	for _, reading := range sample.Temperatures {
		if reading.Valid {
			validTemperatures = append(validTemperatures, reading.ValueDeciC)
		}
	}

	if len(validTemperatures) == 0 {
		return currentLimits{}
	}

	minTemperature := validTemperatures[0]
	maxTemperature := validTemperatures[0]
	for _, temperature := range validTemperatures[1:] {
		if temperature < minTemperature {
			minTemperature = temperature
		}
		if temperature > maxTemperature {
			maxTemperature = temperature
		}
	}

	charge := scaleCurrent(cfg.RatedChargeCurrentMA,
		lookupPermille(cfg.ChargeVoltageTable, maxVoltage))
	charge = minInt64(charge, scaleCurrent(cfg.RatedChargeCurrentMA,
		lookupPermille(cfg.ChargeTemperatureTable, maxTemperature)))
	charge = minInt64(charge, scaleCurrent(cfg.RatedChargeCurrentMA,
		lookupPermille(cfg.ChargeTemperatureTable, minTemperature)))

	discharge := scaleCurrent(cfg.RatedDischargeCurrentMA,
		lookupPermille(cfg.DischargeVoltageTable, minVoltage))
	discharge = minInt64(discharge, scaleCurrent(cfg.RatedDischargeCurrentMA,
		lookupPermille(cfg.DischargeTemperatureTable, maxTemperature)))
	discharge = minInt64(discharge, scaleCurrent(cfg.RatedDischargeCurrentMA,
		lookupPermille(cfg.DischargeTemperatureTable, minTemperature)))

	if len(validTemperatures) < len(sample.Temperatures) {
		charge /= 2
		discharge /= 2
	}

	return currentLimits{charge: charge, discharge: discharge}
}

func (st *batteryState) observeOvercurrent(cfg Config, sample Sample, limits currentLimits) {
	var currentMagnitude int64
	var allowedMagnitude int64
	var direction FaultReason
	switch {
	case sample.CurrentMA > 0:
		currentMagnitude = sample.CurrentMA
		allowedMagnitude = limits.charge
		direction = FaultOvercurrentCharge
	case sample.CurrentMA < 0:
		currentMagnitude = -sample.CurrentMA
		allowedMagnitude = limits.discharge
		direction = FaultOvercurrentDischarge
	}

	excess := currentMagnitude - allowedMagnitude
	if excess <= 0 || (st.overcurrentStartMS >= 0 && st.overcurrentDirection != direction) {
		st.overcurrentStartMS = -1
		st.overcurrentDirection = ""
	}
	if excess <= 0 {
		return
	}

	tier := cfg.OvercurrentTiers[0]
	for _, candidate := range cfg.OvercurrentTiers[1:] {
		if excess >= candidate.ExcessMA {
			tier = candidate
		}
	}
	if st.overcurrentStartMS < 0 {
		st.overcurrentStartMS = sample.TimeMS
		st.overcurrentDirection = direction
	}
	if durationReached(st.overcurrentStartMS, sample.TimeMS, tier.ToleranceMS) {
		st.latch(direction)
	}
}

func (st *batteryState) evaluate(cfg Config, sample Sample) currentLimits {
	minVoltage := sample.VoltagesMV[0]
	maxVoltage := sample.VoltagesMV[0]
	for _, voltage := range sample.VoltagesMV[1:] {
		if voltage < minVoltage {
			minVoltage = voltage
		}
		if voltage > maxVoltage {
			maxVoltage = voltage
		}
	}

	st.sensorFault = !anyTemperatureValid(sample)
	st.observeVoltageProtections(cfg, sample, maxVoltage, minVoltage)
	limits := calculateLimits(cfg, sample, minVoltage, maxVoltage)
	if st.chargeProhibited {
		limits.charge = 0
	}
	if st.dischargeProhibited {
		limits.discharge = 0
	}
	st.observeDelta(cfg, sample, absoluteDifference(maxVoltage, minVoltage))
	st.observeOvercurrent(cfg, sample, limits)
	if st.latched {
		limits.charge = 0
		limits.discharge = 0
	}
	return limits
}

func anyTemperatureValid(sample Sample) bool {
	for _, reading := range sample.Temperatures {
		if reading.Valid {
			return true
		}
	}
	return false
}
