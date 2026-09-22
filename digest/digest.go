// Package digest computes and compares request-body fingerprints.
//
// A fingerprint identifies the content of a request body: the same
// content always yields the same fingerprint, and different content
// yields a different fingerprint. The package depends on nothing else
// in this module.
package digest

import "crypto/sha256"

// Digest is the fingerprint of a request body. It is a value type and
// safe to compare with == or Equal.
type Digest [sha256.Size]byte

// Of returns the fingerprint of body.
func Of(body []byte) Digest {
	return Digest(sha256.Sum256(body))
}

// Equal reports whether two fingerprints are identical, i.e. whether
// they were computed from identical contents.
func (d Digest) Equal(other Digest) bool {
	return d == other
}

// String renders the fingerprint as hex, for logging and debugging.
func (d Digest) String() string {
	const hexdigits = "0123456789abcdef"
	var buf [sha256.Size * 2]byte
	for i, b := range d {
		buf[i*2] = hexdigits[b>>4]
		buf[i*2+1] = hexdigits[b&0x0f]
	}
	return string(buf[:])
}
