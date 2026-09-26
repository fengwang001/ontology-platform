// Package sha implements the SHA-256 compression function and an
// incremental Merkle–Damgård hasher built on package sched.
package sha

import (
	"encoding/binary"
	"errors"
	"sync"

	"ontology/sched"
)

// Sentinel errors; each rejected operation maps to exactly one.
var (
	ErrFinalized = errors.New("sha: write after finalize")
	ErrLength    = errors.New("sha: message length exceeds 2^61 bytes")
)

// MaxBytes is the message length limit: at 2^61 bytes the 64-bit
// bit-length field of the padding would overflow.
const MaxBytes = uint64(1) << 61

// Hasher is an incremental SHA-256 state. The zero value is not usable;
// construct with New. All methods are safe for concurrent use.
type Hasher struct {
	mu     sync.Mutex
	state  [8]uint32
	buf    [sched.BlockSize]byte
	buflen int
	total  uint64 // bytes accepted so far
	done   bool
	digest [32]byte
	blocks int    // blocks compressed by the most recent Write
	limit  uint64 // byte limit, MaxBytes for New
}

// New returns a Hasher with the standard 2^61-byte limit.
func New() *Hasher { return NewWithLimit(MaxBytes) }

// NewWithLimit is New with a custom byte limit; it exists so tests and
// demos can exercise the length-overflow rejection without 2^61 bytes.
func NewWithLimit(limit uint64) *Hasher {
	h := &Hasher{limit: limit}
	h.Reset()
	return h
}

// Reset returns the Hasher to its initial state, keeping the limit.
func (h *Hasher) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = sched.IV
	h.buf = [sched.BlockSize]byte{}
	h.buflen, h.total, h.blocks = 0, 0, 0
	h.done = false
	h.digest = [32]byte{}
}

// Write feeds p into the hash. Rejections (finalized, length overflow)
// return a sentinel error and leave every field untouched.
func (h *Hasher) Write(p []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return ErrFinalized
	}
	if uint64(len(p)) > h.limit-h.total {
		return ErrLength
	}
	h.total += uint64(len(p))
	h.blocks = 0
	for len(p) > 0 {
		n := copy(h.buf[h.buflen:], p)
		h.buflen += n
		p = p[n:]
		if h.buflen == sched.BlockSize {
			compress(&h.state, h.buf[:])
			h.blocks++
			h.buflen = 0
		}
	}
	return nil
}

// Finalize pads, compresses the tail, and returns the 32-byte digest.
// It is idempotent: after finalization it returns a copy of the cached
// digest without recomputing.
func (h *Hasher) Finalize() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.done {
		bitlen := h.total << 3
		h.buf[h.buflen] = 0x80
		h.buflen++
		if h.buflen > 56 {
			h.padTo(sched.BlockSize)
			compress(&h.state, h.buf[:])
			h.buflen = 0
		}
		h.padTo(56)
		binary.BigEndian.PutUint64(h.buf[56:], bitlen)
		compress(&h.state, h.buf[:])
		for i, v := range h.state {
			binary.BigEndian.PutUint32(h.digest[4*i:], v)
		}
		h.done = true
	}
	out := make([]byte, 32)
	copy(out, h.digest[:])
	return out
}

func (h *Hasher) padTo(n int) {
	for h.buflen < n {
		h.buf[h.buflen] = 0
		h.buflen++
	}
}

// compress runs the 64-round compression of one 64-byte block,
// folding the result into state.
func compress(state *[8]uint32, block []byte) {
	var w [64]uint32
	sched.Expand(block, &w)
	a, b, c, d, e, f, g, hh := state[0], state[1], state[2], state[3], state[4], state[5], state[6], state[7]
	for t := 0; t < 64; t++ {
		t1 := hh + sched.Big1(e) + sched.Ch(e, f, g) + sched.K[t] + w[t]
		t2 := sched.Big0(a) + sched.Maj(a, b, c)
		hh, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	state[0] += a
	state[1] += b
	state[2] += c
	state[3] += d
	state[4] += e
	state[5] += f
	state[6] += g
	state[7] += hh
}
