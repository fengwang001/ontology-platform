package battery

type batteryState struct {
	hasSample            bool
	lastTimeMS           int64
	chargeProhibited     bool
	dischargeProhibited  bool
	overvoltageEntryMS   int64
	overvoltageClearMS   int64
	undervoltageEntryMS  int64
	undervoltageClearMS  int64
	balancingRequested   bool
	deltaRunStartMS      int64
	overcurrentStartMS   int64
	overcurrentDirection FaultReason
	sensorFault          bool
	latched              bool
	acceptedAfterLatch   bool
	latchReasons         []FaultReason
}

func newBatteryState() batteryState {
	return batteryState{
		overvoltageEntryMS:   -1,
		overvoltageClearMS:   -1,
		undervoltageEntryMS:  -1,
		undervoltageClearMS:  -1,
		deltaRunStartMS:      -1,
		overcurrentStartMS:   -1,
		overcurrentDirection: "",
	}
}

func durationReached(startMS, nowMS, requiredMS int64) bool {
	if startMS < 0 {
		return false
	}
	return nowMS-startMS >= requiredMS
}

func (st *batteryState) observeVoltageProtections(cfg Config, sample Sample, maxVoltage, minVoltage int64) {
	overvoltageNow := maxVoltage >= cfg.OvervoltageThresholdMV
	overvoltageClearNow := maxVoltage < cfg.OvervoltageThresholdMV-cfg.RecoveryHysteresisMV
	if !st.chargeProhibited {
		if overvoltageNow {
			if st.overvoltageEntryMS < 0 {
				st.overvoltageEntryMS = sample.TimeMS
			}
			if durationReached(st.overvoltageEntryMS, sample.TimeMS, cfg.ConfirmationDurationMS) {
				st.chargeProhibited = true
				st.overvoltageEntryMS = -1
			}
		} else {
			st.overvoltageEntryMS = -1
		}
	} else if overvoltageClearNow {
		if st.overvoltageClearMS < 0 {
			st.overvoltageClearMS = sample.TimeMS
		}
		if durationReached(st.overvoltageClearMS, sample.TimeMS, cfg.ConfirmationDurationMS) {
			st.chargeProhibited = false
			st.overvoltageClearMS = -1
		}
	} else {
		st.overvoltageClearMS = -1
	}

	undervoltageNow := minVoltage <= cfg.UndervoltageThresholdMV
	undervoltageClearNow := minVoltage > cfg.UndervoltageThresholdMV+cfg.RecoveryHysteresisMV
	if !st.dischargeProhibited {
		if undervoltageNow {
			if st.undervoltageEntryMS < 0 {
				st.undervoltageEntryMS = sample.TimeMS
			}
			if durationReached(st.undervoltageEntryMS, sample.TimeMS, cfg.ConfirmationDurationMS) {
				st.dischargeProhibited = true
				st.undervoltageEntryMS = -1
			}
		} else {
			st.undervoltageEntryMS = -1
		}
	} else if undervoltageClearNow {
		if st.undervoltageClearMS < 0 {
			st.undervoltageClearMS = sample.TimeMS
		}
		if durationReached(st.undervoltageClearMS, sample.TimeMS, cfg.ConfirmationDurationMS) {
			st.dischargeProhibited = false
			st.undervoltageClearMS = -1
		}
	} else {
		st.undervoltageClearMS = -1
	}
}

func (st *batteryState) observeDelta(cfg Config, sample Sample, deltaVoltage int64) {
	st.balancingRequested = deltaVoltage > cfg.DeltaVoltageLimitMV
	deltaFaultNow := st.balancingRequested && sample.CurrentMA > cfg.IdleCurrentThresholdMA
	if deltaFaultNow {
		if st.deltaRunStartMS < 0 {
			st.deltaRunStartMS = sample.TimeMS
		}
		if durationReached(st.deltaRunStartMS, sample.TimeMS, cfg.ConfirmationDurationMS) {
			st.latch(FaultDeltaVoltage)
		}
	} else {
		st.deltaRunStartMS = -1
	}
}

func (st *batteryState) latch(reason FaultReason) {
	for _, existing := range st.latchReasons {
		if existing == reason {
			return
		}
	}
	st.latchReasons = append(st.latchReasons, reason)
	st.latched = true
}
