// Package api is the outward-facing surface of the ontology PowMod service.
package api

import (
	"errors"
	"fmt"
	"math/big"

	"ontology/mod"
	"ontology/pow"
)

// API is a stateless handle; safe for concurrent use.
type API struct{}

// New returns a ready-to-use API handle.
func New() *API { return &API{} }

// PowMod returns base^exp mod mod, always in [0, mod).
func (a *API) PowMod(base, exp, m int64) (int64, error) {
	return pow.PowMod(base, exp, m)
}

var triples = [][3]int64{
	{-2, 3, 5}, {-3, 2, 7}, {-3, 3, 7}, {7, 100, 13},
	{-5, 3, 9}, {2, 0, 5}, {5, 0, 1}, {2, 10, 1},
}

// SelfCheck verifies the four invariants against built-in triples.
func (a *API) SelfCheck() error {
	for _, t := range triples {
		got, err := a.PowMod(t[0], t[1], t[2])
		if err != nil {
			return fmt.Errorf("selfcheck: %v", err)
		}
		if got < 0 || got >= t[2] { // invariant 2: result in [0, mod)
			return fmt.Errorf("selfcheck: %v out of [0,%d)", got, t[2])
		}
	}
	// invariant 1: matches an exact big.Int reference on small exponents
	for b := int64(-5); b <= 5; b++ {
		for e := int64(0); e <= 12; e++ {
			for m := int64(1); m <= 17; m++ {
				got, _ := a.PowMod(b, e, m)
				want := new(big.Int).Mod(new(big.Int).Exp(big.NewInt(b), big.NewInt(e), nil), big.NewInt(m))
				want.Mod(want.Add(want, big.NewInt(m)), big.NewInt(m))
				if got != want.Int64() {
					return fmt.Errorf("selfcheck: naive mismatch at (%d,%d,%d)", b, e, m)
				}
			}
		}
	}
	// invariant 3: exponent split composes via mulmod
	for _, t := range triples {
		p1, _ := a.PowMod(t[0], t[1]/2, t[2])
		p2, _ := a.PowMod(t[0], t[1]-t[1]/2, t[2])
		full, _ := a.PowMod(t[0], t[1], t[2])
		if mod.Mulmod(p1, p2, t[2]) != full {
			return fmt.Errorf("selfcheck: split mismatch at %v", t)
		}
	}
	// invariant 4: three distinct, decidable sentinel errors
	_, e0 := a.PowMod(1, 1, 0)
	_, eN := a.PowMod(1, 1, -1)
	_, eX := a.PowMod(1, -1, 1)
	if !errors.Is(e0, pow.ErrModZero) || !errors.Is(eN, pow.ErrModNegative) ||
		!errors.Is(eX, pow.ErrExpNegative) || errors.Is(e0, eN) || errors.Is(e0, eX) || errors.Is(eN, eX) {
		return fmt.Errorf("selfcheck: sentinel errors not distinct")
	}
	return nil
}
