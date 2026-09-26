// Package api is the public face of AES-128 single-block encryption. It wraps
// the lower-level aes package and exposes decidable sentinel errors.
package api

import (
	"errors"

	"ontology/aes"
)

// Sentinel errors are mutually distinct and comparable with errors.Is.
var (
	ErrInvalidKey    = errors.New("api: invalid key length (want 16 bytes)")
	ErrInvalidBlock  = errors.New("api: invalid block length (want 16 bytes)")
	ErrUninitialized = errors.New("api: cipher has no expanded key")
)

// Cipher is safe for concurrent use: encryption only reads the key schedule
// cached at construction; the zero value is an uninitialized cipher.
type Cipher struct {
	c *aes.Cipher
}

// NewCipher validates the key and expands the schedule exactly once.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 16 {
		return nil, ErrInvalidKey
	}
	inner, err := aes.New(key)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return &Cipher{c: inner}, nil
}

// EncryptBlock rejects invalid input (and the uninitialized/zero cipher)
// before touching any state, so a rejected call leaves the cipher usable.
func (c *Cipher) EncryptBlock(block []byte) ([]byte, error) {
	if c == nil || c.c == nil {
		return nil, ErrUninitialized
	}
	if len(block) != 16 {
		return nil, ErrInvalidBlock
	}
	return c.c.EncryptBlock(block)
}

var (
	fipsKey = []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	fipsPT  = []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	fipsCT  = []byte{0x69, 0xc4, 0xe0, 0xd8, 0x6a, 0x7b, 0x04, 0x30, 0xd8, 0xcd, 0xb7, 0x80, 0x70, 0xb4, 0xc5, 0x5a}
)

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SelfCheck verifies, on built-in inputs, the four invariants: the FIPS-197
// vector, agreement with the naive round-by-round reference, one-time key
// expansion (zero recomputed words at m = 100/1000/10000), and the three
// distinct error classes with state left intact after rejection.
func (c *Cipher) SelfCheck() error {
	if c == nil || c.c == nil {
		return ErrUninitialized
	}
	ref, err := NewCipher(fipsKey)
	if err != nil {
		return err
	}
	ct, err := ref.EncryptBlock(fipsPT)
	if err != nil || !equal(ct, fipsCT) {
		return errors.New("api: FIPS-197 vector mismatch")
	}
	for i := 0; i < 16; i++ {
		k, p := make([]byte, 16), make([]byte, 16)
		for j := range k {
			k[j], p[j] = byte(i*7+j*3), byte(i*13+j*5)
		}
		cc, _ := NewCipher(k)
		got, e1 := cc.EncryptBlock(p)
		want, e2 := aes.NaiveEncrypt(k, p)
		if e1 != nil || e2 != nil || !equal(got, want) {
			return errors.New("api: naive reference mismatch")
		}
	}
	for _, m := range []int{100, 1000, 10000} {
		if !c.c.ScheduleReused(m) {
			return errors.New("api: round key was recomputed")
		}
	}
	if _, e := NewCipher(make([]byte, 15)); !errors.Is(e, ErrInvalidKey) {
		return errors.New("api: bad key not rejected")
	}
	if _, e := c.EncryptBlock(make([]byte, 15)); !errors.Is(e, ErrInvalidBlock) {
		return errors.New("api: bad block not rejected")
	}
	var zero Cipher
	if _, e := zero.EncryptBlock(fipsPT); !errors.Is(e, ErrUninitialized) {
		return errors.New("api: uninitialized cipher not rejected")
	}
	after, e := c.EncryptBlock(fipsPT) // still usable, state unchanged
	if e != nil || !equal(after, fipsCT) {
		return errors.New("api: cipher state changed after rejection")
	}
	return nil
}
