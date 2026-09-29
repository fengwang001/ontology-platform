package objectcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Ciphertext format (self-describing so old data stays readable after
// rotation):

// encv1:<decimal key version>:<base64 nonce>:<base64 gcm ciphertext>
const ciphertextPrefix = "encv1:"

// Cipher performs authenticated encryption with an explicit key version
// attached to every ciphertext. Each Encrypt call uses a fresh random nonce;
// nonces already observed under a key are tracked and rejected, so the same
// plaintext always yields a different but deterministically decryptable blob.
type Cipher struct {
	ring *KeyRing

	mu         sync.Mutex
	usedNonces map[string]map[string]struct{}
}

func NewCipher(ring *KeyRing) *Cipher {
	return &Cipher{ring: ring, usedNonces: make(map[string]map[string]struct{})}
}

// Encrypt seals plaintext under the ring's active key.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	version, key := c.ring.activeKey()
	if key == nil {
		return nil, ErrUnknownKeyVersion
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Generate a fresh random nonce and reject the (astronomically unlikely)
	// reuse for this key rather than silently weakening the key.
	var nonce []byte
	c.mu.Lock()
	seen := c.usedNonces[strconv.Itoa(version)]
	for range 4 {
		nonce = make([]byte, gcm.NonceSize())
		if _, err := readRandom(nonce); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		if _, dup := seen[string(nonce)]; !dup {
			if seen == nil {
				seen = make(map[string]struct{})
				c.usedNonces[strconv.Itoa(version)] = seen
			}
			seen[string(nonce)] = struct{}{}
			c.mu.Unlock()
			break
		}
		nonce = nil
	}
	if nonce == nil {
		c.mu.Unlock()
		return nil, ErrNonceReuse
	}

	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	return encode(version, nonce, sealed), nil
}

// Decrypt opens a ciphertext using the key version recorded in the blob. A
// missing key version means rotation dropped data the ring must keep, which is
// reported distinctly from other failures.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	version, nonce, sealed, err := decode(blob)
	if err != nil {
		return nil, err
	}
	key, ok := c.ring.get(version)
	if !ok {
		return nil, fmt.Errorf("%w: version %d", ErrUnknownKeyVersion, version)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func encode(version int, nonce, ciphertext []byte) []byte {
	return []byte(ciphertextPrefix +
		strconv.Itoa(version) + ":" +
		base64Std(nonce) + ":" +
		base64Std(ciphertext))
}

func decode(blob []byte) (int, []byte, []byte, error) {
	s := string(blob)
	if !strings.HasPrefix(s, ciphertextPrefix) {
		return 0, nil, nil, errors.New("objectcrypto: unrecognized ciphertext envelope")
	}
	parts := strings.Split(strings.TrimPrefix(s, ciphertextPrefix), ":")
	if len(parts) != 3 {
		return 0, nil, nil, errors.New("objectcrypto: malformed ciphertext envelope")
	}
	version, err := strconv.Atoi(parts[0])
	if err != nil || version <= 0 {
		return 0, nil, nil, errors.New("objectcrypto: malformed key version")
	}
	nonce, err := base64Decode(parts[1])
	if err != nil {
		return 0, nil, nil, errors.New("objectcrypto: malformed nonce")
	}
	ciphertext, err := base64Decode(parts[2])
	if err != nil {
		return 0, nil, nil, errors.New("objectcrypto: malformed ciphertext payload")
	}
	return version, nonce, ciphertext, nil
}

// KeyVersionOf returns the key version recorded in a ciphertext envelope.
func KeyVersionOf(blob []byte) (int, error) {
	version, _, _, err := decode(blob)
	return version, err
}
