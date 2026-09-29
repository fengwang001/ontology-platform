// Package addr derives stable, collision-resistant content addresses.
// It depends on no other package of this module.
package addr

import (
	"crypto/sha256"
	"encoding/hex"
)

// Size is the address length in bytes (SHA-256).
const Size = sha256.Size

// Addr is the content address of a chunk.
type Addr [Size]byte

// Of returns the address of data: SHA-256 over its exact bytes.
// The function is pure: equal bytes always yield an equal address and
// different bytes yield distinct addresses (up to SHA-256 collision bounds).
func Of(data []byte) Addr {
	return sha256.Sum256(data)
}

// String renders the address as lowercase hex.
func (a Addr) String() string {
	return hex.EncodeToString(a[:])
}

// Equal reports whether two addresses are identical.
func (a Addr) Equal(b Addr) bool { return a == b }
