// Package digest computes and compares fingerprints of request bodies.
// Identical content yields identical fingerprints; different content
// yields different fingerprints (with cryptographic probability).
package digest

import (
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprint is a comparable content hash of a request body.
type Fingerprint [sha256.Size]byte

// Of returns the fingerprint of the given request body.
func Of(body []byte) Fingerprint {
	return Fingerprint(sha256.Sum256(body))
}

// Equal reports whether two fingerprints are identical.
func (f Fingerprint) Equal(other Fingerprint) bool {
	return f == other
}

// String returns the hex encoding of the fingerprint.
func (f Fingerprint) String() string {
	return hex.EncodeToString(f[:])
}
