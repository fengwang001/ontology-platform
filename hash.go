package ontology

import "hash/fnv"

// hashBytes returns a deterministic 64-bit digest of data.
//
// FNV-1a is used instead of hash/maphash: maphash's default seed is chosen
// randomly per process, which would make ring layouts differ across runs and
// across independently constructed rings. FNV-1a is a pure function of its
// input, so the same nodes always land on the same positions, in any process,
// in any insertion order.
func hashBytes(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}

// hashString hashes a string without an intermediate allocation.
func hashString(s string) uint64 {
	return hashBytes([]byte(s))
}

// vnodePosition is the position of replica r of node id on the ring. The
// replica index is mixed into the hashed key so that a node's vnodes spread
// across the whole ring rather than colliding at one point.
func vnodePosition(id string, replica int) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	h.Write([]byte{
		byte(replica),
		byte(replica >> 8),
		byte(replica >> 16),
		byte(replica >> 24),
	})
	return h.Sum64()
}
