// Package ontology implements a run-length encoded (RLE) bitmap set over
// the full uint32 universe. The set is stored as a normalized list of
// alternating runs, and all set operations (Union/Intersect/Difference)
// are performed directly on the compressed run representation.
//
// Canonical encoding (Bytes):
//
//	Bytes = uvarint(runCount) || per-run uvarint(length<<1 | value)
//
// where value is 0 or 1. The encoding is unique for a given set because
// the run list is kept normalized: adjacent same-value runs are merged,
// zero-length runs never appear, and trailing zero runs are dropped.
//
// The empty set encodes as the single byte 0x00 (runCount = 0), which is
// the shortest possible deterministic encoding.
//
// Run lengths are uint64 in memory, so a single run may span the whole
// 2^32 universe (length = 1<<32). Such a run is encoded as
// uvarint(1<<33 | value), which fits in 6 bytes; no 32-bit overflow is
// possible anywhere because all length arithmetic is done in uint64.
//
// All methods are safe for concurrent use.
package ontology
