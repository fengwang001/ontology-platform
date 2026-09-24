// Package stat computes share statistics and fairness deviation metrics.
package stat

// Shares normalizes values (served cost or weights) into fractions that
// sum to 1. A zero total yields an empty map.
func Shares(values map[string]float64) map[string]float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	out := make(map[string]float64, len(values))
	if total == 0 {
		return out
	}
	for k, v := range values {
		out[k] = v / total
	}
	return out
}

// Deviation returns |actual-expected|/expected per tenant. Tenants with
// zero expected share are skipped.
func Deviation(actual, expected map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(expected))
	for k, e := range expected {
		if e == 0 {
			continue
		}
		d := actual[k] - e
		if d < 0 {
			d = -d
		}
		out[k] = d / e
	}
	return out
}

// MaxDeviation returns the largest relative deviation between the actual
// served-cost shares and the weight-proportional expected shares.
func MaxDeviation(served, weights map[string]float64) float64 {
	dev := Deviation(Shares(served), Shares(weights))
	max := 0.0
	for _, d := range dev {
		if d > max {
			max = d
		}
	}
	return max
}
