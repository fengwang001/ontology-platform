// Package sha implements the SHA-256 compression function and Merkle–Damgård
// incremental hashing: full blocks compress as they fill, cached state reused.
package sha

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"

	"ontology/sched"
)

// maxBytes is the largest message whose bit length fits the 64-bit field.
const maxBytes = uint64(1) << 61

// Sentinel errors, pairwise distinct; callers decide with errors.Is.
var (
	ErrOverflow  = errors.New("sha: message length overflow (>= 2^61 bytes)")
	ErrFinalized = errors.New("sha: hasher already finalized")
)

// Hasher is the incremental SHA-256 state; use New (zero value not ready).
type Hasher struct {
	state      [8]uint32
	buf        [64]byte
	bufLen     int
	total      uint64 // total accepted message bytes
	finalized  bool
	digest     [32]byte
	lastBlocks int // blocks compressed by the most recent Write (unexported)
}

// New returns a hasher seeded with the IV.
func New() *Hasher { h := &Hasher{}; h.Reset(); return h }

// Reset restores the IV/empty-stream state; the instance is reusable.
func (h *Hasher) Reset() {
	h.state, h.bufLen, h.total = sched.IV, 0, 0
	h.finalized, h.digest, h.lastBlocks = false, [32]byte{}, 0
}

// compressBlock compresses one 64-byte block; returns the new state (add-back).
func compressBlock(state [8]uint32, block []byte) [8]uint32 {
	var w [64]uint32
	sched.LoadBlock(&w, block)
	sched.Expand(&w)
	a, b, c, d := state[0], state[1], state[2], state[3]
	e, f, g, hh := state[4], state[5], state[6], state[7]
	for t := 0; t < 64; t++ {
		t1 := hh + sched.BigSigma1(e) + sched.Ch(e, f, g) + sched.K[t] + w[t]
		t2 := sched.BigSigma0(a) + sched.Maj(a, b, c)
		hh, g, f, e = g, f, e, d+t1
		d, c, b, a = c, b, a, t1+t2
	}
	return [8]uint32{state[0] + a, state[1] + b, state[2] + c, state[3] + d,
		state[4] + e, state[5] + f, state[6] + g, state[7] + hh}
}

// Write feeds bytes, rejecting state-free a finalized hasher or bit-length overflow.
func (h *Hasher) Write(p []byte) error {
	if h.finalized {
		return ErrFinalized
	}
	if uint64(len(p)) > maxBytes-h.total { // validate before any mutation
		return ErrOverflow
	}
	h.lastBlocks = 0
	n := len(p)
	for len(p) > 0 {
		c := copy(h.buf[h.bufLen:], p)
		h.bufLen, p = h.bufLen+c, p[c:]
		if h.bufLen == len(h.buf) {
			h.state, h.bufLen, h.lastBlocks = compressBlock(h.state, h.buf[:]), 0, h.lastBlocks+1
		}
	}
	h.total += uint64(n)
	return nil
}

// Finalize pads the tail (0x80, zeros, 64-bit bit length) and caches the digest.
func (h *Hasher) Finalize() ([32]byte, error) {
	if h.finalized {
		return h.digest, ErrFinalized
	}
	st := h.state
	var first [64]byte
	copy(first[:], h.buf[:h.bufLen])
	first[h.bufLen] = 0x80
	bits := h.total << 3
	if h.bufLen < 56 {
		binary.BigEndian.PutUint64(first[56:], bits)
		st = compressBlock(st, first[:])
	} else {
		st = compressBlock(st, first[:]) // 0x80 then zeros to end of block
		var second [64]byte
		binary.BigEndian.PutUint64(second[56:], bits)
		st = compressBlock(st, second[:])
	}
	for i, v := range st {
		binary.BigEndian.PutUint32(h.digest[4*i:], v)
	}
	h.finalized = true
	return h.digest, nil
}

// SelfCheck verifies the four built-in invariants; see NOTES.md.
func SelfCheck() error {
	abc, _ := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	h0 := New()
	_ = h0.Write([]byte("abc"))
	if d, _ := h0.Finalize(); !bytes.Equal(d[:], abc) {
		return errors.New("sha: abc vector mismatch")
	}
	for _, n := range []int{0, 1, 3, 55, 56, 63, 64, 65, 127, 128, 1000} {
		msg := make([]byte, n)
		for j := range msg {
			msg[j] = byte(j*7 + 1)
		}
		one, inc := New(), New()
		_ = one.Write(msg)
		for _, b := range msg {
			_ = inc.Write([]byte{b})
		}
		got, e1 := one.Finalize()
		got2, e2 := inc.Finalize()
		if e1 != nil || e2 != nil || got != got2 {
			return errors.New("sha: one-shot/incremental mismatch")
		}
	}
	r := New() // rejected post-finalize write must leave no trace
	_ = r.Write([]byte("abc"))
	d0, _ := r.Finalize()
	if r.Write([]byte{1}) != ErrFinalized {
		return errors.New("sha: post-finalize write accepted")
	}
	r.Reset()
	_ = r.Write([]byte("abc"))
	if d1, _ := r.Finalize(); d0 != d1 {
		return errors.New("sha: state trace after rejection")
	}
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		_ = h.Write(make([]byte, m*64))
		if e := h.Write([]byte{1}); e != nil || h.lastBlocks != 0 {
			return errors.New("sha: trailing byte recompressed blocks")
		}
	}
	return nil
}
