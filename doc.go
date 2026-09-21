// Package ontology implements k-way merge set operations over sorted
// float64 streams.
//
// Each input stream is a []float64 sorted in non-decreasing order under
// the usual float64 total order (with +0.0 and -0.0 considered equal).
// Union, Intersect and Difference are computed with a single k-way merge
// driven by one min-heap whose size never exceeds the number of streams;
// memory usage is O(number of streams), independent of stream lengths.
//
// Equality: +0.0 and -0.0 are the same value. NaN is not equal to
// anything, including itself, so NaN may not participate in set
// operations: any NaN in the input produces a *NaNError locating the
// stream and element index. Positive and negative infinity are legal
// elements and compare normally.
//
// Ordering: every stream is validated before merging. A stream that
// contains a strict decrease (s[j-1] > s[j]) produces an *OrderError
// locating the stream and the index j of the offending element. Equal
// adjacent elements are allowed (multiset inputs are expected).
//
// Semantics: Set semantics emit each distinct value at most once.
// Multiset semantics emit, per distinct value: for Union the maximum
// per-stream count, for Intersect the minimum per-stream count, and for
// Difference the count in the first stream minus the sum of the counts
// in the remaining streams, floored at zero.
//
// Edge cases: Union and Difference of zero streams yield an empty
// (non-nil) result. Intersect of zero streams is mathematically
// ambiguous and returns ErrNoStreams. Empty streams are legal and
// contribute nothing. A single stream yields itself, deduplicated
// under Set semantics and copied verbatim under Multiset semantics.
// Inputs are never modified; results are freshly allocated slices.
package ontology
