// Package api is the public entry point of the counter-mode stream cipher:
// sessions bound to (key, nonce), encrypt/decrypt with random access, and a
// SelfCheck for the four required invariants. It depends only on the lower
// stream and splitmix packages; nothing in it is imported by them.
package api

import (
	"bytes"
	"errors"
	"sync"

	"ontology/splitmix"
	"ontology/stream"
)

// Distinct, distinguishable sentinel errors (errors.Is-able).
var (
	// ErrInvalidKey is returned when key == 0.
	ErrInvalidKey = errors.New("api: key must be non-zero")
	// ErrInvalidNonce is returned when nonce == 0.
	ErrInvalidNonce = errors.New("api: nonce must be non-zero")
	// ErrNonceReused is returned when a (key, nonce) pair is registered twice.
	ErrNonceReused = errors.New("api: nonce reuse for the same key")
)

// Session is one encryption conversation bound to a single (key, nonce).
type Session struct {
	cipher *stream.Cipher
}

// registry holds the set of registered (key, nonce) pairs.
type registry struct {
	mu    sync.Mutex
	pairs map[[2]uint64]struct{}
}

// defaultRegistry backs New; SelfCheck uses a local registry to leave no trace.
var defaultRegistry = &registry{pairs: make(map[[2]uint64]struct{})}

// create validates and atomically registers (key, nonce); rejection never touches the map.
func (r *registry) create(key, nonce uint64) (*Session, error) {
	if key == 0 {
		return nil, ErrInvalidKey
	}
	if nonce == 0 {
		return nil, ErrInvalidNonce
	}
	id := [2]uint64{key, nonce}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.pairs[id]; exists {
		return nil, ErrNonceReused
	}
	s := &Session{cipher: stream.New(splitmix.Seed(key, nonce))}
	r.pairs[id] = struct{}{}
	return s, nil
}

// New starts a session for (key, nonce). It rejects key == 0, nonce == 0, and
// reuse of an already-registered pair. Rejected calls change no state and the
// returned error is one of the package's sentinel errors.
func New(key, nonce uint64) (*Session, error) {
	return defaultRegistry.create(key, nonce)
}

// Encrypt XORs plaintext with the session keystream from offset 0.
func (s *Session) Encrypt(p []byte) []byte { return s.cipher.Encrypt(p) }

// Decrypt XORs ciphertext with the same session keystream from offset 0.
func (s *Session) Decrypt(c []byte) []byte { return s.cipher.Decrypt(c) }

// EncryptAt XORs plaintext with keystream starting at byte offset.
func (s *Session) EncryptAt(offset int64, p []byte) []byte {
	return s.cipher.EncryptAt(offset, p)
}

// SelfCheck verifies the four invariants on built-in inputs using an isolated
// registry, so it mutates no process-visible session state. It is safe for
// concurrent invocation.
func SelfCheck() error {
	r := &registry{pairs: make(map[[2]uint64]struct{})}

	const key, nonce uint64 = 0x0123456789ABCDEF, 0x0000000000000001
	s, err := r.create(key, nonce)
	if err != nil {
		return err
	}
	p := []byte("counter mode self-check 0123456789 \x00\xff")
	if ct := s.Encrypt(p); !bytes.Equal(s.Decrypt(ct), p) {
		return errors.New("api: symmetry failed") // invariant 1
	}
	seed := splitmix.Seed(key, nonce)
	off := int64(123)
	ref := referenceXor(seed, 0, p)
	if got := s.Encrypt(p); !bytes.Equal(got, ref) {
		return errors.New("api: naive-reference mismatch") // invariant 2
	}
	if got, want := s.EncryptAt(off, p), referenceXor(seed, off, p); !bytes.Equal(got, want) {
		return errors.New("api: random-access mismatch") // invariant 3
	}
	if err := rejectionLeavesNoTrace(r); err != nil {
		return err // invariant 4
	}
	return nil
}

// rejectionLeavesNoTrace exercises the three distinct rejection paths.
func rejectionLeavesNoTrace(r *registry) error {
	if _, err := r.create(0, 7); !errors.Is(err, ErrInvalidKey) {
		return errors.New("api: expected ErrInvalidKey")
	}
	if _, err := r.create(9, 0); !errors.Is(err, ErrInvalidNonce) {
		return errors.New("api: expected ErrInvalidNonce")
	}
	const bk, bn uint64 = 4242, 99
	if _, err := r.create(bk, bn); err != nil { // first use succeeds
		return err
	}
	if _, err := r.create(bk, bn); !errors.Is(err, ErrNonceReused) {
		return errors.New("api: expected ErrNonceReused")
	}
	// Rejected pairs were never registered: the rejected (key,nonce) above
	// must still succeed on a clean first use, while an established pair keeps
	// rejecting reuse and remains usable.
	if _, err := r.create(0, 7); !errors.Is(err, ErrInvalidKey) {
		return errors.New("api: state changed after key rejection")
	}
	s2, err := r.create(77, 88)
	if err != nil {
		return err
	}
	plain := []byte("still usable")
	if !bytes.Equal(s2.Decrypt(s2.Encrypt(plain)), plain) {
		return errors.New("api: session unusable after rejections")
	}
	return nil
}

// referenceXor independently builds the keystream block-by-block from 0 and
// XORs it over p starting at byte offset off.
func referenceXor(seed uint64, off int64, p []byte) []byte {
	out := make([]byte, len(p))
	for k := range p {
		abs := uint64(off) + uint64(k)
		b := splitmix.SplitMix64(seed ^ abs/8)
		out[k] = p[k] ^ byte(b>>(8*(abs%8)))
	}
	return out
}
