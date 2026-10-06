package battery

// protection owns the two voltage protection states (charge ban from
// over-voltage, discharge ban from under-voltage). Each state is a small state
// machine driven by two independent sustain trackers:
//
//	entry:   any cell crossing the line (>= OV / <= UV), held ConfirmDurationMS
//	release: every cell clearing the line by RecoveryHystMV, held equally long
//
// While a ban is active it only clears through the release path; while clear
// it only sets through the entry path. A condition failing once invalidates
// its run (sustain semantics).
type protection struct {
	cfg *Config

	ovEnter   sustain
	ovRelease sustain
	uvEnter   sustain
	uvRelease sustain

	banCharge    bool
	banDischarge bool
}

func newProtection(cfg *Config) *protection { return &protection{cfg: cfg} }

func (p *protection) update(s Sample) (banCharge, banDischarge bool) {
	cfg := p.cfg
	anyOV, allOVClear := false, true
	anyUV, allUVClear := false, true
	for _, v := range s.CellVoltagesMV {
		if v >= cfg.OverVoltageMV {
			anyOV = true
		}
		if v <= cfg.UnderVoltageMV {
			anyUV = true
		}
		// Release demands every cell to cross beyond the line plus hysteresis:
		// over-voltage release needs all cells below OV-hyst, under-voltage
		// release needs all cells above UV+hyst.
		if v > cfg.OverVoltageMV-cfg.RecoveryHystMV {
			allOVClear = false
		}
		if v < cfg.UnderVoltageMV+cfg.RecoveryHystMV {
			allUVClear = false
		}
	}

	dur := cfg.ConfirmDurationMS
	if p.banCharge {
		if p.ovRelease.observe(s.TimeMS, allOVClear, dur) {
			p.banCharge = false
			p.ovEnter.reset()
		}
	} else if p.ovEnter.observe(s.TimeMS, anyOV, dur) {
		p.banCharge = true
		p.ovRelease.reset()
	}

	if p.banDischarge {
		if p.uvRelease.observe(s.TimeMS, allUVClear, dur) {
			p.banDischarge = false
			p.uvEnter.reset()
		}
	} else if p.uvEnter.observe(s.TimeMS, anyUV, dur) {
		p.banDischarge = true
		p.uvRelease.reset()
	}

	return p.banCharge, p.banDischarge
}
