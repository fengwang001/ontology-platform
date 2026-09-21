// Package ontology merges already-sorted float64 streams into their
// union, intersection, or difference, in a single k-way merge pass.
//
// Inputs are slices sorted in non-decreasing order under the usual
// float64 total order (with the equality contract below). The
// implementation never sorts, copies wholesale, or mutates its inputs;
// it keeps only one cursor and one in-flight heap element per stream, so
// extra memory is proportional to the number of streams, not their
// lengths.
//
// Equality contract:
//   - +0.0 and -0.0 are the same value.
//   - NaN is not equal to anything, including itself, so it cannot take
//     part in set operations: any NaN element aborts the operation with
//     a *NaNError locating the stream and element index.
//   - +Inf and -Inf are ordinary values and participate normally.
//
// Ordering contract: every stream must be non-decreasing. Equal adjacent
// elements are legal (they are how multisets are expressed); a strictly
// decreasing adjacent pair aborts with an *UnsortedError locating the
// stream and the index of the first out-of-order element.
//
// Edge cases (defined behavior):
//   - Zero streams: Union is the empty set; Intersect and Difference are
//     mathematically undefined and return ErrNoStreams.
//   - One stream: Union and Intersect return that stream's values
//     (deduplicated under Set semantics); Difference returns them
//     unchanged, as there is nothing to subtract.
//   - An empty stream: ignored by Union; forces Intersect to be empty;
//     an empty first stream makes Difference empty.
//   - All streams empty: Union, Intersect, and Difference are all empty.
//
// Result slices are freshly allocated and share no backing storage with
// the inputs.
package ontology
