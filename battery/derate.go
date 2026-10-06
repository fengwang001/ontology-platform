package battery

import "fmt"

// derateCalc owns the pure limit-table logic. For each direction the allowed
// current is the minimum of: the rated current, the voltage-table result and
// the temperature-table result. Invalid temperature points are ignored; with
// some (but not all) points invalid the surviving limit is halved (floor); if
// every point is invalid both directions are forced to zero.
type derateCalc struct {
	cfg *Config
}

func newDerateCalc(cfg *Config) *derateCalc { return &derateCalc{cfg: cfg} }

func (d *derateCalc) limits(s Sample) (chargeMA, dischargeMA int64, sensorFault bool) {
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

	minT, maxT := int64(0), int64(0)
	valid := 0
	for _, t := range s.Temperatures {
		if t == InvalidTemperature || t < cfg.MinTemperature || t > cfg.MaxTemperature {
			continue
		}
		if valid == 0 {
			minT, maxT = t, t
		} else {
			if t < minT {
				minT = t
			}
			if t > maxT {
				maxT = t
			}
		}
		valid++
	}

	charge := cfg.RatedChargeMA
	discharge := cfg.RatedDischargeMA

	charge = min64(charge, ratedOf(cfg.RatedChargeMA, lookupTier(cfg.ChargeVoltageTable, maxV)))
	discharge = min64(discharge, ratedOf(cfg.RatedDischargeMA, lookupTier(cfg.DischargeVoltageTable, minV)))

	if valid == 0 {
		return 0, 0, true
	}

	chargeTemp := min64(
		ratedOf(cfg.RatedChargeMA, lookupTier(cfg.ChargeTempTable, minT)),
		ratedOf(cfg.RatedChargeMA, lookupTier(cfg.ChargeTempTable, maxT)),
	)
	dischargeTemp := min64(
		ratedOf(cfg.RatedDischargeMA, lookupTier(cfg.DischargeTempTable, minT)),
		ratedOf(cfg.RatedDischargeMA, lookupTier(cfg.DischargeTempTable, maxT)),
	)
	charge = min64(charge, chargeTemp)
	discharge = min64(discharge, dischargeTemp)

	if valid < cfg.TempCount {
		charge /= 2
		discharge /= 2
	}
	return charge, discharge, false
}

func ratedOf(rated int64, permille int32) int64 {
	return rated * int64(permille) / 1000
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (d *derateCalc) explain(s Sample) string {
	return fmt.Sprintf("derate over %d cells/%d temps", len(s.CellVoltagesMV), len(s.Temperatures))
}
