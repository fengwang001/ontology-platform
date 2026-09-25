// Package rnd computes the deterministic Rendezvous (HRW) weight
// w(key, node) and resolves ownership by maximum weight with
// lexicographically-smallest-ID tie-break. It depends on no other package.
package rnd

import (
	"encoding/binary"
	"hash/fnv"
)

// Weight returns the deterministic weight w(key, node).
//
// It is FNV-1a over a length-prefixed encoding of key and node, so it
// depends solely on its arguments. There is no random seed, PID, clock or
// call-order input: the same pair always yields the same weight, in every
// process, on every machine.
func Weight(key, node string) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(len(key)))
	h.Write(buf[:])
	h.Write([]byte(key))
	binary.LittleEndian.PutUint64(buf[:], uint64(len(node)))
	h.Write(buf[:])
	h.Write([]byte(node))
	return h.Sum64()
}

// Best returns the ID of the node with the greatest weight for key and that
// weight. Equal weights break to the lexicographically smallest node ID.
// ok is false when nodes is empty.
func Best(key string, nodes []string) (id string, w uint64, ok bool) {
	for _, n := range nodes {
		wn := Weight(key, n)
		if !ok || wn > w || (wn == w && n < id) {
			id, w, ok = n, wn, true
		}
	}
	return id, w, ok
}
