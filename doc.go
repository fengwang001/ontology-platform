// Package ontology provides a concurrency-safe, mergeable online
// statistics accumulator for float64 samples.
//
// The accumulator tracks count, mean, population variance and sample
// variance in O(1) space using Welford's online recurrence:
//
//	n     = n + 1
//	delta = x - mean
//	mean  = mean + delta/n
//	M2    = M2 + delta*(x-mean)
//
// Population variance is M2/n and sample variance is M2/(n-1). This
// avoids the numerically catastrophic "sum of squares minus square of
// mean" two-pass formula, which loses all low-order bits when the mean
// is large relative to the spread (e.g. samples near 1e9).
//
// Two independent accumulators are combined with the parallel-variance
// merge of Chan et al.:
//
//	n     = na + nb
//	delta = meanB - meanA
//	mean  = (meanA*na + meanB*nb) / n
//	M2    = M2A + M2B + delta*delta*na*nb/n
//
// The merge expression is written so that Merge(a, b) and Merge(b, a)
// produce bit-for-bit identical results: IEEE 754 multiplication and
// addition are commutative, and negation is exact, so swapping the
// operands only flips the sign of delta, which the square cancels.
// Merging with an empty accumulator is an exact identity.
//
// NaN samples are rejected and counted in a skipped counter. A
// positive or negative infinity is accepted as a sample but poisons
// the accumulator: every subsequent statistic read fails with
// ErrStatsUnavailable instead of silently returning NaN.
package ontology
