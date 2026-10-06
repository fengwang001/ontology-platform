// Package certselector implements a server-name based TLS certificate selector.
package certselector

// KeyType is the public key algorithm type of a certificate.
type KeyType int

const (
	// ECDSA is an elliptic curve key.
	ECDSA KeyType = iota
	// RSA is an RSA key.
	RSA
)

// Certificate is an immutable certificate entry.
type Certificate struct {
	ID        string
	Names     []string
	Key       KeyType
	NotBefore int64
	NotAfter  int64
}
