// Package digest computes and compares fingerprints of request bodies.
//
// A fingerprint is a content-derived tag: identical bodies always produce
// identical fingerprints, and different bodies produce different
// fingerprints (up to the collision resistance of SHA-256). The package
// depends on nothing outside the standard library.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprint is the content fingerprint of a request body. It is a
// comparable value type, so fingerprints can be used as map keys and
// compared with ==, though Equal is provided for readability.
type Fingerprint [sha256.Size]byte

// Of returns the fingerprint of the given request body. The empty body
// (nil or zero-length) has a well-defined fingerprint like any other.
func Of(body []byte) Fingerprint {
	return Fingerprint(sha256.Sum256(body))
}

// OfString is a convenience wrapper around Of for string bodies.
func OfString(body string) Fingerprint {
	return Of([]byte(body))
}

// Equal reports whether two fingerprints were derived from the same
// content. It never inspects the original bodies.
func (f Fingerprint) Equal(other Fingerprint) bool {
	return f == other
}

// String renders the fingerprint as lowercase hex, for logs and tests.
func (f Fingerprint) String() string {
	return hex.EncodeToString(f[:])
}
