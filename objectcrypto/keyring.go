package objectcrypto

import (
	"crypto/rand"
	"sync"
)

const keySize = 32 // AES-256

// KeyVersion is one named key revision held by the KeyRing.
type KeyVersion struct {
	Version int
	Key     []byte
}

// KeyRing stores keys separately from encrypted data. Every key version that
// has ever encrypted data is retained so that ciphertext written before a
// rotation stays decryptable. Only the active (newest) version is used to
// encrypt new writes.
type KeyRing struct {
	mu        sync.RWMutex
	keys      map[int][]byte
	active    int
	hasActive bool
}

func NewKeyRing() *KeyRing {
	return &KeyRing{keys: make(map[int][]byte)}
}

// GenerateKey returns a fresh AES-256 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

func validateKey(version int, key []byte) error {
	if version <= 0 {
		return ErrUnknownKeyVersion
	}
	if len(key) != keySize {
		return ErrUnknownKeyVersion
	}
	return nil
}

// Add registers an existing key version without making it active. It is used
// to import historical keys so that old ciphertext remains readable.
func (r *KeyRing) Add(version int, key []byte) error {
	if err := validateKey(version, key); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := append([]byte(nil), key...)
	if existing, ok := r.keys[version]; ok {
		// A version number is immutable: replacing a key would make the data
		// encrypted under the old key undecryptable.
		if !constantTimeEqual(existing, copied) {
			return ErrKeyRotatedUnreadable
		}
		return nil
	}
	r.keys[version] = copied
	return nil
}

// Rotate registers the new key version and marks it active for all future
// encryption. Previous versions are retained, never overwritten or removed.
func (r *KeyRing) Rotate(version int, key []byte) error {
	if err := validateKey(version, key); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasActive && version <= r.active {
		// Rotation may only move forward; reusing an old version would make
		// existing ciphertext ambiguous/unreadable.
		return ErrKeyRotatedUnreadable
	}
	copied := append([]byte(nil), key...)
	if existing, ok := r.keys[version]; ok {
		if !constantTimeEqual(existing, copied) {
			return ErrKeyRotatedUnreadable
		}
	} else {
		r.keys[version] = copied
	}
	r.active = version
	r.hasActive = true
	return nil
}

// Retire always fails: dropping a key version would make ciphertext that
// references it permanently unreadable, which the component must reject.
func (r *KeyRing) Retire(version int) error {
	return ErrKeyRotatedUnreadable
}

// Versions returns the retained key versions in ascending order.
func (r *KeyRing) Versions() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]int, 0, len(r.keys))
	for v := range r.keys {
		out = append(out, v)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func (r *KeyRing) get(version int) ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[version]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), key...), true
}

func (r *KeyRing) activeKey() (int, []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.hasActive {
		return 0, nil
	}
	return r.active, append([]byte(nil), r.keys[r.active]...)
}

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
