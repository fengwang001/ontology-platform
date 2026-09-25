// Package stat computes share statistics and fairness metrics over
// executed-cost ledgers.
package stat

// Shares normalizes a per-tenant cost ledger into fractions of the
// total. A zero total yields zero shares.
func Shares(costs map[string]float64) map[string]float64 {
	total := 0.0
	for _, c := range costs {
		total += c
	}
	out := make(map[string]float64, len(costs))
	for id, c := range costs {
		if total > 0 {
			out[id] = c / total
		}
	}
	return out
}

// RelDeviation returns, per tenant, the relative deviation between the
// actual executed-cost share and the theoretical weight share:
// |actual - want| / want. Tenants present in weights but absent from
// costs count as zero actual share.
func RelDeviation(weights, costs map[string]float64) map[string]float64 {
	want := Shares(weights)
	got := Shares(costs)
	out := make(map[string]float64, len(weights))
	for id, w := range want {
		if w == 0 {
			continue
		}
		d := got[id] - w
		if d < 0 {
			d = -d
		}
		out[id] = d / w
	}
	return out
}

// MaxRelDeviation is the worst per-tenant relative deviation.
func MaxRelDeviation(weights, costs map[string]float64) float64 {
	max := 0.0
	for _, d := range RelDeviation(weights, costs) {
		if d > max {
			max = d
		}
	}
	return max
}
