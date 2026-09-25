// Package stat measures how actual executed-cost shares deviate from the
// fair shares implied by tenant weights.
package stat

import "math"

// Deviation returns, per tenant, the relative deviation of the actual
// executed-cost share from the weight-implied fair share:
// (actual - want) / want. Zero means perfectly fair.
func Deviation(weights, cost map[string]float64) map[string]float64 {
	var wSum, cSum float64
	for _, w := range weights {
		wSum += w
	}
	for _, c := range cost {
		cSum += c
	}
	dev := make(map[string]float64, len(weights))
	for id, w := range weights {
		want := w / wSum
		got := cost[id] / cSum
		dev[id] = (got - want) / want
	}
	return dev
}

// MaxAbs returns the largest absolute deviation across tenants.
func MaxAbs(dev map[string]float64) float64 {
	m := 0.0
	for _, d := range dev {
		m = math.Max(m, math.Abs(d))
	}
	return m
}
