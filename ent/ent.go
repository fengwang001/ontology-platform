// Package ent defines a single audit entry and the hash-chain primitives.
// It depends on nothing outside the standard library.
package ent

import (
	"crypto/sha256"
	"encoding/binary"
)

// Entry is one immutable audit record.
type Entry struct {
	Seq  int64
	TS   int64
	Who  string
	Op   string
	Hash [32]byte
}

// GenesisHash is the fixed hash stored in the genesis entry.
func GenesisHash() [32]byte { return sha256.Sum256([]byte("genesis")) }

// Genesis returns the genesis entry: Seq=0, TS=0, Who="genesis", Op="init".
func Genesis() Entry {
	return Entry{Seq: 0, TS: 0, Who: "genesis", Op: "init", Hash: GenesisHash()}
}

// ComputeHash derives an entry's hash as
// sha256(prev.Hash ‖ Seq ‖ TS ‖ Who ‖ Op) with fixed separators/encoding.
func ComputeHash(prev [32]byte, seq, ts int64, who, op string) [32]byte {
	var num [8]byte
	h := sha256.New()
	h.Write(prev[:])
	binary.BigEndian.PutUint64(num[:], uint64(seq))
	h.Write(num[:])
	binary.BigEndian.PutUint64(num[:], uint64(ts))
	h.Write(num[:])
	h.Write([]byte(who))
	h.Write([]byte{0}) // fixed field separator
	h.Write([]byte(op))
	h.Write([]byte{0})
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
