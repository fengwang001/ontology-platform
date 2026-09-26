// Package api is the outward face of the SHA-256 hasher.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/sha"
)

// ErrSize rejects a truncation size outside 1..32.
var ErrSize = errors.New("api: size out of range [1,32]")

// Hasher is a SHA-256 hasher whose Finalize output is truncated to
// Size bytes.
type Hasher struct {
	h    *sha.Hasher
	size int
}

// New returns a Hasher, or ErrSize if size is not in 1..32.
func New(size int) (*Hasher, error) {
	if size < 1 || size > 32 {
		return nil, ErrSize
	}
	return &Hasher{h: sha.New(), size: size}, nil
}

// Write feeds p; rejections (sha.ErrFinalized, sha.ErrLength) leave no trace.
func (h *Hasher) Write(p []byte) error { return h.h.Write(p) }

// Finalize returns the digest truncated to Size bytes; idempotent.
func (h *Hasher) Finalize() []byte { return h.h.Finalize()[:h.size] }

// Reset makes the Hasher writable again from a clean state.
func (h *Hasher) Reset() { h.h.Reset() }

// Size returns the truncation size fixed at New.
func (h *Hasher) Size() int { return h.size }

var abcDigest = []byte{
	0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40, 0xde, 0x5d, 0xae, 0x22, 0x23,
	0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad,
}

// SelfCheck verifies the four invariants of the spec on built-in
// inputs and returns the first failure, or nil. It is stateless and
// safe for concurrent use.
func SelfCheck() error {
	// Invariant 2: known vector.
	h, err := New(32)
	if err != nil {
		return err
	}
	if err := h.Write([]byte("abc")); err != nil {
		return err
	}
	if !bytes.Equal(h.Finalize(), abcDigest) {
		return errors.New("selfcheck: abc vector mismatch")
	}
	// Invariants 1 & 3: one-shot == incremental at every split point.
	msg := make([]byte, 300)
	for i := range msg {
		msg[i] = byte(i*7 + 3)
	}
	one, _ := New(32)
	if err := one.Write(msg); err != nil {
		return err
	}
	ref := one.Finalize()
	for cut := 0; cut <= len(msg); cut++ {
		two, _ := New(32)
		if err := two.Write(msg[:cut]); err != nil {
			return err
		}
		if err := two.Write(msg[cut:]); err != nil {
			return err
		}
		if !bytes.Equal(two.Finalize(), ref) {
			return fmt.Errorf("selfcheck: split at %d mismatch", cut)
		}
	}
	// Invariant 4: rejections leave no trace and stay distinguishable.
	if _, err := New(0); !errors.Is(err, ErrSize) {
		return errors.New("selfcheck: size 0 not rejected")
	}
	if _, err := New(33); !errors.Is(err, ErrSize) {
		return errors.New("selfcheck: size 33 not rejected")
	}
	if err := h.Write([]byte("x")); !errors.Is(err, sha.ErrFinalized) {
		return errors.New("selfcheck: write after finalize not rejected")
	}
	if !bytes.Equal(h.Finalize(), abcDigest) {
		return errors.New("selfcheck: rejected write left a trace")
	}
	low := sha.NewWithLimit(9)
	if err := low.Write([]byte("12345678")); err != nil {
		return err
	}
	if err := low.Write([]byte("90")); !errors.Is(err, sha.ErrLength) {
		return errors.New("selfcheck: length overflow not rejected")
	}
	if err := low.Write([]byte("a")); err != nil { // still usable afterwards
		return err
	}
	h.Reset()
	if err := h.Write([]byte("abc")); err != nil {
		return err
	}
	if !bytes.Equal(h.Finalize(), abcDigest) {
		return errors.New("selfcheck: reset did not restore usability")
	}
	return nil
}
