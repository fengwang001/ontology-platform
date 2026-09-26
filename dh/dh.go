// Package dh implements Diffie-Hellman key exchange over the cyclic
// group defined by a prime p and a generator g: parameter validation,
// public key generation, shared secret derivation, and key validation.
// It depends only on modarith. A Group is immutable after creation, so
// all methods are safe for concurrent use and rejected operations can
// never leave a trace.
package dh

import (
	"errors"

	"ontology/modarith"
)

// Sentinel errors, one per rejection class; distinguishable with errors.Is.
var (
	ErrBadGroupParams = errors.New("dh: invalid group parameters (need p>=2 and 2<=g<=p-1)")
	ErrBadPrivateKey  = errors.New("dh: invalid private key (need 1<=priv<=p-2)")
	ErrBadPublicKey   = errors.New("dh: invalid public key (need 2<=pub<=p-2)")
)

// Group holds validated, immutable DH group parameters.
type Group struct {
	p uint64
	g uint64
}

// NewGroup validates p and g and returns the group, or ErrBadGroupParams.
func NewGroup(p, g uint64) (*Group, error) {
	if p < 2 || g < 2 || g > p-1 {
		return nil, ErrBadGroupParams
	}
	return &Group{p: p, g: g}, nil
}

// Params returns the group's prime and generator.
func (gr *Group) Params() (p, g uint64) {
	return gr.p, gr.g
}

// ValidPrivate reports whether a is a legal private key: 1 <= a <= p-2.
func (gr *Group) ValidPrivate(a uint64) bool {
	return a >= 1 && a <= gr.p-2
}

// ValidPublic reports whether pub is an acceptable peer public key:
// 2 <= pub <= p-2 (1 and p-1 are rejected to block small-subgroup
// confinement of the shared secret).
func (gr *Group) ValidPublic(pub uint64) bool {
	return pub >= 2 && pub <= gr.p-2
}

// PublicKey returns A = g^a mod p, or ErrBadPrivateKey.
func (gr *Group) PublicKey(a uint64) (uint64, error) {
	if !gr.ValidPrivate(a) {
		return 0, ErrBadPrivateKey
	}
	return modarith.ModPow(gr.g, a, gr.p), nil
}

// Secret returns S = peerPub^a mod p. Both inputs are validated before
// any computation; a rejection changes nothing.
func (gr *Group) Secret(a, peerPub uint64) (uint64, error) {
	if !gr.ValidPrivate(a) {
		return 0, ErrBadPrivateKey
	}
	if !gr.ValidPublic(peerPub) {
		return 0, ErrBadPublicKey
	}
	return modarith.ModPow(peerPub, a, gr.p), nil
}
