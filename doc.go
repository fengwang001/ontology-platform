// Package ontology implements k-way set operations over sorted streams.
//
// Each input stream is a []float64 already sorted in ascending order.
// Union, Intersect and Difference are computed with a single k-way merge
// driven by one min-heap whose size never exceeds the number of streams,
// so extra memory is O(number of streams), independent of stream length.
//
// Semantics: every operation comes in two flavors.
//   - Set: each distinct value appears at most once in the result.
//   - Multiset: Union takes the per-stream maximum occurrence count,
//     Intersect takes the minimum, Difference takes the count in the
//     first stream minus the counts in all other streams, floored at 0.
//
// Equality: +0.0 and -0.0 are the same value. NaN never equals anything,
// not even itself, so NaN is rejected with a NaNError locating the exact
// stream and index. +-Inf are ordinary, legal elements.
//
// Ordering: every stream is validated while merging. A strictly
// decreasing adjacent pair yields an UnsortedError locating the stream
// and index; equal adjacent elements are legal (multiset duplicates).
//
// Edge cases:
//   - Zero streams: Union and Difference return an empty (non-nil)
//     slice; Intersect returns ErrNoStreams because the intersection of
//     an empty family is undefined.
//   - One stream: all three operations return that stream, deduplicated
//     under Set semantics and verbatim under Multiset semantics.
//   - An empty stream contributes nothing; Intersect with any empty
//     stream is empty. All streams empty behaves accordingly.
//
// Results are freshly allocated; input slices are never modified.
//
// Comparison bound: with N total elements and k streams, the merge
// performs at most N*(3*ceil(log2(k)) + 3) element comparisons: each
// element is pushed once (<= ceil(log2(k)) sift-up comparisons), popped
// once (<= 2*ceil(log2(k)) sift-down comparisons), checked once for
// group equality and once for sortedness.
package ontology
