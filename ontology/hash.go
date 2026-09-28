package hotcount

// FNV-1a 64-bit constants.
const (
	fnvOffset64 = uint64(14695981039346656037)
	fnvPrime64  = uint64(1099511628211)
)

// bucket maps an element to a column in the given row.
//
// The row index is mixed into the FNV-1a state before hashing the eight
// little-endian bytes of the element, so every row uses an independent,
// deterministic hash function. The mapping never changes across processes,
// which keeps candidate results reproducible.
func bucket(element uint64, row, width int) int {
	h := fnvOffset64
	h ^= uint64(row) + 1
	h *= fnvPrime64
	for shift := uint(0); shift < 64; shift += 8 {
		h ^= (element >> shift) & 0xff
		h *= fnvPrime64
	}
	return int(h % uint64(width))
}
