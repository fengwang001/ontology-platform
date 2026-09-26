// Package stream generates the counter-mode keystream and XORs it with
// plaintext/ciphertext. Every block is derived independently from its index
// (block_i = splitmix64(seed ^ i)), so keystream bytes are reachable by
// random access without generating earlier blocks.
package stream

import (
	"sync"

	"ontology/splitmix"
)

// Cipher holds one session's seed. The keystream is stateless, but the
// block counter below is updated by every crypt operation, so all methods
// are guarded by mu.
type Cipher struct {
	mu   sync.Mutex
	seed uint64
	// blocksComputed counts the blocks actually computed by the most recent
	// XORAt call. It is deliberately unexported and is never returned by any
	// exported function or method.
	blocksComputed uint64
}

// New builds a keystream generator from an already-derived seed.
func New(seed uint64) *Cipher {
	return &Cipher{seed: seed}
}

// XORAt XORs src with keystream bytes starting at absolute stream offset
// (in bytes) and returns a freshly allocated result. Encryption and
// decryption are the same operation. Blocks are reached directly: only the
// blocks overlapping [offset, offset+len(src)) are computed, each once.
// offset must be non-negative.
func (c *Cipher) XORAt(offset int64, src []byte) []byte {
	dst := make([]byte, len(src))

	c.mu.Lock()
	defer c.mu.Unlock()

	var n uint64
	idx := uint64(offset) / 8
	var block uint64
	for k := range src {
		pos := uint64(offset) + uint64(k)
		bIdx := pos / 8
		if k == 0 || bIdx != idx {
			idx = bIdx
			block = splitmix.SplitMix64(c.seed ^ idx)
			n++
		}
		dst[k] = src[k] ^ byte(block>>(8*(pos%8)))
	}
	c.blocksComputed = n
	return dst
}

// RandomAccessCostsOneBlock reports whether encrypting a single byte at
// byte offset 8*m computes exactly one block, for every given m. It returns
// only the verdict; the internal counter value is not exposed.
func RandomAccessCostsOneBlock(seed uint64, ms []int64) bool {
	c := New(seed)
	for _, m := range ms {
		c.XORAt(8*m, []byte{0})
		c.mu.Lock()
		one := c.blocksComputed == 1
		c.mu.Unlock()
		if !one {
			return false
		}
	}
	return true
}
