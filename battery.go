package microgrid

// battery owns the physical storage parameters, SoC dynamics and
// maintenance throughput. It holds no plans or forecasts.
type battery struct {
	params     Params
	soc        int
	throughput int
}

func newBattery(p Params, initialSOC int) *battery {
	return &battery{params: p, soc: initialSOC}
}

// chargeStored converts delivered charge energy into energy actually stored.
// Floor division by the loss denominator applies.
func (b *battery) chargeStored(chargeAmount int) int {
	return chargeAmount * (b.params.LossDenominator - b.params.LossNumerator) / b.params.LossDenominator
}

// nextSoC returns the end-of-slot SoC for an actual/simulated action.
func (b *battery) nextSoC(soc int, a Action, amount int) int {
	switch a {
	case Charge:
		return soc + b.chargeStored(amount)
	case Discharge:
		return soc - amount
	default:
		return soc
	}
}

// addThroughput accumulates actual discharge and reports whether the
// maintenance threshold is reached or crossed.
func (b *battery) addThroughput(amount int) bool {
	b.throughput += amount
	return b.throughput >= b.params.MaintenanceThreshold
}
