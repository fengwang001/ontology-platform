// Package ontology implements k-way merge based set operations over
// already-sorted float64 streams.
//
// Each input stream must be sorted in non-decreasing order under the same
// comparison rule. Equal adjacent elements are allowed (multiset
// semantics); a strictly decreasing adjacent pair is reported as an
// *OrderError. NaN is never a valid element and is reported as a
// *NaNError. +0.0 and -0.0 are the same value; results always carry +0.0.
// +Inf and -Inf are ordinary elements.
//
// Two semantics are supported and never mixed:
//   - Set: every distinct value appears at most once in the result.
//   - Multiset: Union takes the per-stream maximum multiplicity,
//     Intersect the minimum, Difference the first stream's multiplicity
//     minus the sum of the others, floored at zero.
//
// Edge cases (zero or empty inputs):
//   - Union of zero streams: defined as the empty (non-nil) result.
//   - Intersect of zero streams: mathematically ambiguous, returns
//     ErrEmptyIntersect.
//   - Difference of zero streams: no minuend exists, returns
//     ErrEmptyDifference.
//   - A single stream: Union/Intersect/Difference all return that
//     stream's values under the requested semantics.
//   - An empty stream among others: ignored by Union; forces Intersect
//     to be empty; subtracts nothing in Difference.
//   - All streams empty (but at least one stream): all three operations
//     return the empty (non-nil) result.
//
// Every operation performs exactly one k-way merge with a min-heap whose
// size never exceeds the number of streams; extra memory is O(streams),
// independent of stream lengths. Input slices are never modified and the
// result is freshly allocated.
package ontology
