// Package api is the public entry point: sessions with nonce-reuse
// protection, encrypt/decrypt, random access and a built-in self-check.
package api

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/splitmix"
	"ontology/stream"
)

// Sentinel errors, pairwise distinct; match them with errors.Is.
var (
	ErrInvalidKey   = errors.New("api: key must be non-zero")
	ErrInvalidNonce = errors.New("api: nonce must be non-zero")
	ErrNonceReuse   = errors.New("api: (key, nonce) pair already used by another session")
)

type pair struct {
	key   uint64
	nonce uint64
}

var (
	regMu      sync.Mutex
	registered = map[pair]struct{}{}
	// selfCheckNonce hands out unique nonces to concurrent SelfCheck runs.
	selfCheckNonce uint64 = 1 << 40
)

// Session is one (key, nonce) keystream session.
type Session struct {
	cipher *stream.Cipher
}

// New validates inputs and rejects reuse of a known pair before registering
// it; a rejected call never touches the registry or existing sessions.
func New(key, nonce uint64) (*Session, error) {
	if key == 0 {
		return nil, ErrInvalidKey
	}
	if nonce == 0 {
		return nil, ErrInvalidNonce
	}
	p := pair{key, nonce}
	regMu.Lock()
	defer regMu.Unlock()
	if _, seen := registered[p]; seen {
		return nil, ErrNonceReuse
	}
	s := &Session{cipher: stream.New(splitmix.Seed(key, nonce))}
	registered[p] = struct{}{}
	return s, nil
}

// Encrypt XORs the plaintext with the keystream from offset 0.
func (s *Session) Encrypt(p []byte) []byte { return s.cipher.XORAt(0, p) }

// Decrypt XORs the ciphertext with the same keystream from offset 0.
func (s *Session) Decrypt(c []byte) []byte { return s.cipher.XORAt(0, c) }

// EncryptAt XORs p with the keystream from absolute byte offset (random access).
func (s *Session) EncryptAt(offset int64, p []byte) []byte {
	return s.cipher.XORAt(offset, p)
}

// SelfCheck verifies the four invariants. Concurrent runs draw fresh
// nonces, so they never collide on the global registry.
func SelfCheck() error {
	key := uint64(0x0123456789ABCDEF)
	nonce := atomic.AddUint64(&selfCheckNonce, 1)
	s, err := New(key, nonce)
	if err != nil {
		return err
	}
	for _, p := range [][]byte{nil, {}, []byte("abcdefgh"), []byte("counter mode stream cipher")} {
		if string(s.Decrypt(s.Encrypt(p))) != string(p) {
			return errors.New("api: round-trip mismatch")
		}
	}
	seed := splitmix.Seed(key, nonce)
	if !matchesNaiveReference(s, seed) {
		return errors.New("api: keystream differs from naive reference")
	}
	if !randomAccessConsistent(s, seed) ||
		!stream.RandomAccessCostsOneBlock(seed, []int64{100, 1000, 10000}) {
		return errors.New("api: random-access mismatch")
	}
	if err := failuresLeaveNoTrace(key, nonce); err != nil {
		return err
	}
	return nil
}

func matchesNaiveReference(s *Session, seed uint64) bool {
	for _, off := range []int64{0, 7, 64, 1000} {
		z := make([]byte, 40)
		ks := s.EncryptAt(off, z)
		for k := range ks {
			pos := uint64(off) + uint64(k)
			b := splitmix.SplitMix64(seed ^ (pos / 8))
			if ks[k] != byte(b>>(8*(pos%8))) {
				return false
			}
		}
	}
	return true
}

func randomAccessConsistent(s *Session, seed uint64) bool {
	const off int64 = 1234
	p := []byte("random access at arbitrary offset")
	got := s.EncryptAt(off, p)
	for k := range p {
		pos := uint64(off) + uint64(k)
		b := splitmix.SplitMix64(seed ^ (pos / 8))
		if got[k] != p[k]^byte(b>>(8*(pos%8))) {
			return false
		}
	}
	return true
}

func failuresLeaveNoTrace(key, nonce uint64) error {
	if _, err := New(0, nonce); !errors.Is(err, ErrInvalidKey) {
		return errors.New("api: key==0 not rejected with ErrInvalidKey")
	}
	if _, err := New(key, 0); !errors.Is(err, ErrInvalidNonce) {
		return errors.New("api: nonce==0 not rejected with ErrInvalidNonce")
	}
	if _, err := New(key, nonce); !errors.Is(err, ErrNonceReuse) {
		return errors.New("api: reuse not rejected with ErrNonceReuse")
	}
	// Still rejected; existing session works; a fresh pair can be created.
	if _, err := New(key, nonce); !errors.Is(err, ErrNonceReuse) {
		return errors.New("api: registry changed after rejected reuse")
	}
	fresh := atomic.AddUint64(&selfCheckNonce, 1)
	s2, err := New(key^0x5A5A, fresh)
	if err != nil {
		return err
	}
	if string(s2.Decrypt(s2.Encrypt([]byte("still alive")))) != "still alive" {
		return errors.New("api: session broken after rejected calls")
	}
	return nil
}
