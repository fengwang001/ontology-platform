// Package api is the public surface of the incremental SHA-256 hasher. It
// wraps package sha with a mutex (concurrent Size/SelfCheck/finalized reads
// are race-clean) and truncates the 32-byte digest to the requested size.
package api

import (
	"errors"
	"sync"

	"ontology/sha"
)

// Sentinel errors, pairwise distinct; decide with errors.Is.
var (
	// ErrInvalidSize means New got a size outside 1..32.
	ErrInvalidSize = errors.New("api: size must be in 1..32")
	// ErrOverflow means the message bit length would overflow 64 bits.
	ErrOverflow = sha.ErrOverflow
	// ErrFinalized means Write/Finalize was used after Finalize without Reset.
	ErrFinalized = sha.ErrFinalized
)

// Hasher is the public incremental SHA-256 hasher. Create it with New.
type Hasher struct {
	mu   sync.Mutex
	size int
	impl *sha.Hasher
}

// New returns a hasher whose Finalize output is truncated to size bytes.
// A size outside 1..32 is rejected with ErrInvalidSize and no hasher is made.
func New(size int) (*Hasher, error) {
	if size < 1 || size > 32 {
		return nil, ErrInvalidSize
	}
	return &Hasher{size: size, impl: sha.New()}, nil
}

// Write feeds bytes. A finalized hasher or an overflowing total length is
// rejected without changing any state (buffer, cached state, counters, flag).
func (h *Hasher) Write(p []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.impl.Write(p)
}

// Finalize finalizes the stream and returns the digest truncated to size
// bytes. Calling it again without Reset returns the cached digest together
// with ErrFinalized; concurrent calls on a finalized hasher are race-clean.
func (h *Hasher) Finalize() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d, err := h.impl.Finalize()
	return append([]byte(nil), d[:h.size]...), err
}

// Reset restores the empty-stream state; the hasher is reusable afterwards.
func (h *Hasher) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.impl.Reset()
}

// Size returns the configured output size in bytes.
func (h *Hasher) Size() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.size
}

// SelfCheck verifies the four invariants on built-in inputs: one-shot equals
// incremental feeding, the abc known vector, chunk-split invariance, and
// rejection without state change. It never exposes internal counters.
func SelfCheck() error { return sha.SelfCheck() }
