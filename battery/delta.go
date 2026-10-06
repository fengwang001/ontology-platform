package battery

// deltaTracker handles cell voltage spread. A spread strictly above the limit
// raises a non-latching balance request. A charging current strictly above the
// rest threshold together with the spread, held ConfirmDurationMS, latches a
// voltage-delta fault (reported by the manager).
type deltaTracker struct {
	cfg     *Config
	chargeS sustain
}

func newDeltaTracker(cfg *Config) *deltaTracker { return &deltaTracker{cfg: cfg} }

func (d *deltaTracker) update(s Sample) (balanceRequest, chargingExceededHeld bool) {
	cfg := d.cfg
	minV, maxV := s.CellVoltagesMV[0], s.CellVoltagesMV[0]
	for _, v := range s.CellVoltagesMV[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	spread := maxV - minV
	balance := spread > cfg.VoltageDeltaLimitMV
	charging := s.CurrentMA > cfg.RestCurrentThresholdMA
	held := d.chargeS.observe(s.TimeMS, balance && charging, cfg.ConfirmDurationMS)
	return balance, held
}

func (d *deltaTracker) resetSustain() { d.chargeS.reset() }

func spreadExceeds(volts []int64, limit int64) bool {
	minV, maxV := volts[0], volts[0]
	for _, v := range volts[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	return maxV-minV > limit
}
