// Package rnd implements the deterministic HRW weight function and the
// ownership decision (highest weight; ties broken by the smallest node ID).
// It depends on no other package in this module.
package rnd

import (
	"encoding/binary"
	"hash/fnv"
)

// Weight is the deterministic rendezvous weight w(key, node).
// It is a pure function of (key, node): FNV-1a over length-prefixed
// encodings of both inputs, so the same pair always hashes to the same
// value and no random seed, clock, PID or call order is involved.
func Weight(key, node string) uint64 {
	h := fnv.New64a()
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], uint64(len(key)))
	h.Write(b[:n])
	h.Write([]byte(key))
	n = binary.PutUvarint(b[:], uint64(len(node)))
	h.Write(b[:n])
	h.Write([]byte(node))
	return h.Sum64()
}

// Prefer reports whether candidate (cw, cid) beats the current best
// (bw, bid): strictly greater weight, or equal weight with a
// lexicographically smaller node ID.
func Prefer(cw uint64, cid string, bw uint64, bid string) bool {
	if cw != bw {
		return cw > bw
	}
	return cid < bid
}

// Pick returns the node that owns key among nodes under HRW, using weight
// for w(key, node). Ties go to the lexicographically smallest ID; the
// result does not depend on slice order. The boolean is false for an empty
// node set.
func Pick(key string, nodes []string, weight func(key, node string) uint64) (string, bool) {
	var best string
	var bestW uint64
	for i, id := range nodes {
		w := weight(key, id)
		if i == 0 || Prefer(w, id, bestW, best) {
			best, bestW = id, w
		}
	}
	if len(nodes) == 0 {
		return "", false
	}
	return best, true
}
