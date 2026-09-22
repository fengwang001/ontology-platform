// Package cost defines the cost model: scan cost, join cost and the
// cardinality of intermediate results. All functions are pure.
package cost

// Scan is the cost of a sequential scan of a table with the given rows.
func Scan(rows int64) float64 {
	return float64(rows)
}

// Join is the cost of joining two intermediate results under a nested-loop
// model without indexes: both inputs must be produced (their costs) and the
// nested loop touches leftCard*rightCard pairs.
func Join(leftCard, rightCard, leftCost, rightCost float64) float64 {
	return leftCost + rightCost + leftCard*rightCard
}

// Card estimates the cardinality of a join result: the Cartesian product of
// the input cardinalities scaled by the combined selectivity of all join
// predicates (1.0 when there are none).
func Card(leftCard, rightCard, selectivity float64) float64 {
	return leftCard * rightCard * selectivity
}
