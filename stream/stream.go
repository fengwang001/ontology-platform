// Package stream generates the counter-mode keystream and XORs plaintext or
// ciphertext against it. Block i is splitmix64(seed^i), independently
// addressable, so any byte offset is reached without generating preceding
// blocks. It depends only on the splitmix package.
package stream

import (
	"fmt"
	"sync"

	"ontology/splitmix"
)

// Cipher is one keystream fixed by a derived seed.
type Cipher struct {
	seed uint64

	mu sync.Mutex
	// blocksComputed counts the blocks the most recent xorAt actually
	// computed. It is unexported and is never returned through the API.
	blocksComputed int
}

// New creates a keystream cipher from an already-derived seed.
func New(seed uint64) *Cipher {
	return &Cipher{seed: seed}
}

// block returns keystream block i directly: splitmix64(seed ^ i).
func (c *Cipher) block(i uint64) uint64 {
	return splitmix.SplitMix64(c.seed ^ i)
}

// xorAt XORs data with the keystream starting at byte offset off. Each block
// touched is computed exactly once and counted in blocksComputed. Callers
// must pass a non-negative off.
func (c *Cipher) xorAt(off int64, data []byte) []byte {
	out := make([]byte, len(data))

	c.mu.Lock()
	defer c.mu.Unlock()

	blocks := 0
	for k := 0; k < len(data); {
		abs := uint64(off) + uint64(k)
		blockIndex, byteIndex := abs/8, abs%8
		b := c.block(blockIndex)
		blocks++
		for k < len(data) && byteIndex < 8 {
			out[k] = data[k] ^ byte(b>>(8*byteIndex)) // little-endian byte
			k++
			byteIndex++
		}
	}
	c.blocksComputed = blocks
	return out
}

// Encrypt XORs plaintext with the keystream starting at offset 0.
func (c *Cipher) Encrypt(p []byte) []byte { return c.xorAt(0, p) }

// Decrypt XORs ciphertext with the same keystream starting at offset 0.
func (c *Cipher) Decrypt(ct []byte) []byte { return c.xorAt(0, ct) }

// EncryptAt XORs plaintext with the keystream starting at byte offset off.
func (c *Cipher) EncryptAt(off int64, p []byte) []byte { return c.xorAt(off, p) }

// largeMs are the block-count levels used to prove O(1) random access.
var largeMs = []int64{100, 1000, 5000, 10000}

// RandomAccessCostInvariant verifies internally that encrypting one byte at
// offset 8*m computes exactly one block for every large m. It returns nil on
// success and exposes no counter value, only pass/fail.
func RandomAccessCostInvariant() error {
	c := New(0x9E3779B97F4A7C15)
	for _, m := range largeMs {
		c.EncryptAt(8*m, []byte{0xAA})
		c.mu.Lock()
		n := c.blocksComputed
		c.mu.Unlock()
		if n != 1 {
			return fmt.Errorf("stream: offset %d computed %d blocks, want 1", 8*m, n)
		}
	}
	return nil
}
