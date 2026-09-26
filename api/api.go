// Package api is the outward face of the DH key-exchange module.
// It wraps dh with a stable surface: New, PublicKey, Secret, SelfCheck.
package api

import (
	"fmt"

	"ontology/dh"
	"ontology/modarith"
)

// Re-exported sentinel errors so callers can classify rejections with
// errors.Is without importing dh.
var (
	ErrBadGroupParams = dh.ErrBadGroupParams
	ErrBadPrivateKey  = dh.ErrBadPrivateKey
	ErrBadPublicKey   = dh.ErrBadPublicKey
)

// Exchange is a validated DH group ready for key exchange. Immutable
// after New, so all methods are safe for concurrent use.
type Exchange struct {
	gr *dh.Group
}

// New validates the group parameters and returns an Exchange.
func New(p, g uint64) (*Exchange, error) {
	gr, err := dh.NewGroup(p, g)
	if err != nil {
		return nil, err
	}
	return &Exchange{gr: gr}, nil
}

// PublicKey returns A = g^priv mod p, or ErrBadPrivateKey.
func (x *Exchange) PublicKey(priv uint64) (uint64, error) {
	return x.gr.PublicKey(priv)
}

// Secret returns S = peerPub^priv mod p, or a sentinel error.
func (x *Exchange) Secret(priv, peerPub uint64) (uint64, error) {
	return x.gr.Secret(priv, peerPub)
}

// SelfCheck verifies the four invariants on built-in parameters and
// returns nil only if all of them hold.
func SelfCheck() error {
	x, err := New(29, 2)
	if err != nil {
		return fmt.Errorf("selfcheck setup: %w", err)
	}
	// Invariant 1: exchange consistency, Secret(a,Pub(b)) == Secret(b,Pub(a)).
	for _, ab := range [][2]uint64{{5, 11}, {1, 27}, {13, 7}} {
		pa, _ := x.PublicKey(ab[0])
		pb, _ := x.PublicKey(ab[1])
		sa, _ := x.Secret(ab[0], pb)
		sb, _ := x.Secret(ab[1], pa)
		if sa != sb {
			return fmt.Errorf("selfcheck: exchange mismatch for %v", ab)
		}
	}
	// Invariants 2+3: square-and-multiply matches the naive reference.
	for _, c := range [][3]uint64{{2, 90, 29}, {7, 300, 101}} {
		want := uint64(1)
		for i := uint64(0); i < c[1]; i++ {
			want = modarith.Mul(want, c[0], c[2])
		}
		if got := modarith.ModPow(c[0], c[1], c[2]); got != want {
			return fmt.Errorf("selfcheck: modpow(%d,%d,%d)=%d, naive=%d", c[0], c[1], c[2], got, want)
		}
	}
	// Invariant 4: rejections leave no trace; the exchange still works.
	before, _ := x.PublicKey(5)
	if _, err := New(1, 2); err == nil {
		return fmt.Errorf("selfcheck: bad group params accepted")
	}
	if _, err := x.PublicKey(0); err == nil {
		return fmt.Errorf("selfcheck: bad private key accepted")
	}
	if _, err := x.Secret(5, 1); err == nil {
		return fmt.Errorf("selfcheck: bad public key accepted")
	}
	if after, _ := x.PublicKey(5); after != before {
		return fmt.Errorf("selfcheck: state changed after rejections")
	}
	return nil
}
