package ontology

import "encoding/binary"

// Bytes returns the canonical encoding of the set:
//
//	uvarint(runCount) || per-run uvarint(length<<1 | value)
//
// Because the run list is always normalized, the same set always produces
// the exact same byte sequence regardless of how it was constructed.
// The empty set encodes as the single byte 0x00. A run of length 2^32
// (the whole universe) is encoded as uvarint(1<<33|value) in 6 bytes.
func (s *Set) Bytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	buf := make([]byte, 0, 1+len(s.runs)*binary.MaxVarintLen64)
	buf = binary.AppendUvarint(buf, uint64(len(s.runs)))
	for _, r := range s.runs {
		buf = binary.AppendUvarint(buf, r.length<<1|uint64(r.val))
	}
	return buf
}
