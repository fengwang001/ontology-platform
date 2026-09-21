// Package streamset computes union, intersection, and difference over
// multiple ascending-sorted float64 streams using a single k-way merge.
//
// Inputs must already be sorted in ascending order by the same comparison
// rule. The merge is performed with one min-heap whose size never exceeds
// the number of streams, so extra memory is proportional to the stream
// count and independent of stream lengths.
//
// Equality and ordering:
//   - +0.0 and -0.0 are the same value.
//   - NaN is never equal to anything, including itself, so NaN is rejected
//     with a *NaNError locating the stream and element index.
//   - ±Inf are legal elements and compare normally.
//
// Validation: every stream is checked before merging. A strictly
// decreasing adjacent pair yields an *OrderError locating the stream and
// the index of the later (offending) element. Equal adjacent elements are
// legal (multiset data) and never reported.
//
// Semantics (see Semantics):
//   - Set: each distinct value appears at most once in the result.
//   - Multiset: union takes the per-value maximum count across streams,
//     intersection takes the minimum count (absent counts as zero), and
//     difference takes the first stream's count minus the sum of the
//     others, floored at zero.
//
// Edge cases:
//   - Zero streams: union and difference are defined as empty results;
//     intersection is mathematically ambiguous and returns
//     ErrEmptyIntersection.
//   - One stream: union and intersection return that stream's values
//     (deduplicated under Set semantics); difference returns the stream
//     itself (deduplicated under Set semantics).
//   - Empty streams contribute nothing; if all streams are empty the
//     result is empty.
//
// Results are freshly allocated slices; inputs are never modified.
package streamset
