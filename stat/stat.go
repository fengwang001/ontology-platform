// Package stat computes executed-cost shares and fairness deviation.
package stat

// Shares normalises per-tenant executed cost into fractions of the total.
func Shares(cost map[string]float64) map[string]float64 {
	total := 0.0
	for _, c := range cost {
		total += c
	}
	out := make(map[string]float64, len(cost))
	if total == 0 {
		return out
	}
	for id, c := range cost {
		out[id] = c / total
	}
	return out
}

// WeightShares normalises weights into fractions of the total weight.
func WeightShares(w map[string]float64) map[string]float64 { return Shares(w) }

// RelDeviation returns |actual-expected|/expected for every tenant present
// in expected; a tenant missing from actual counts as fully deviated.
func RelDeviation(actual, expected map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(expected))
	for id, exp := range expected {
		act := actual[id]
		if exp == 0 {
			continue
		}
		d := act - exp
		if d < 0 {
			d = -d
		}
		out[id] = d / exp
	}
	return out
}
