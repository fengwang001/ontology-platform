// Package digest computes and compares request-body fingerprints.
//
// A fingerprint is a content hash: identical bodies always produce the
// same fingerprint, and different bodies produce different fingerprints.
// The package depends on nothing outside the standard library.
package digest

import "crypto/sha256"

// Digest is the fingerprint of a request body.
type Digest [sha256.Size]byte

// Of returns the fingerprint of body.
func Of(body []byte) Digest {
	return Digest(sha256.Sum256(body))
}

// Equal reports whether two fingerprints are identical.
func (d Digest) Equal(other Digest) bool {
	return d == other
}

// String renders the fingerprint as lowercase hex, for logging and demos.
func (d Digest) String() string {
	const hexdigits = "0123456789abcdef"
	var buf [sha256.Size * 2]byte
	for i, b := range d {
		buf[i*2] = hexdigits[b>>4]
		buf[i*2+1] = hexdigits[b&0x0f]
	}
	return string(buf[:])
}
