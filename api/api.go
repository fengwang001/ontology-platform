// Package api is the external move-to-front interface. An MTF instance
// owns a validated alphabet; its whole-sequence Encode/Decode each run on
// a working list that starts from that alphabet's initial order, so
// Decode(Encode(data)) == data always holds, even on the same instance.
// (The step-wise evolving list lives in package mtf; this package depends
// on mtfseq for the whole-sequence transform.)
package api

import (
	"errors"

	"ontology/mtf"
	"ontology/mtfseq"
)

// The three errors are the same values as mtf's, distinguishable by
// errors.Is against either package.
var (
	ErrInvalidAlphabet = mtf.ErrInvalidAlphabet
	ErrUnknownSymbol   = mtf.ErrUnknownSymbol
	ErrInvalidIndex    = mtf.ErrInvalidIndex
)

// MTF is a stateful transform instance. It owns an independent, immutable
// alphabet; per-call working list state is local and discarded, so a
// rejected call leaves the instance unchanged and reusable.
type MTF struct {
	alpha []byte
}

// New validates alpha (non-empty, pairwise distinct) and stores a copy.
func New(alpha []byte) (*MTF, error) {
	if _, err := mtf.New(alpha); err != nil {
		return nil, err
	}
	return &MTF{alpha: append([]byte(nil), alpha...)}, nil
}

// Encode emits each symbol's current index then moves it to front. On the
// first unknown symbol it returns (nil, ErrUnknownSymbol); the discarded
// local working list means instance state is untouched.
func (m *MTF) Encode(data []byte) ([]byte, error) {
	out, err := mtfseq.Encode(data, m.alpha)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Decode emits list[i] per index then moves it front. On the first
// i >= len(alpha) it returns (nil, ErrInvalidIndex), leaving the instance
// untouched.
func (m *MTF) Decode(indices []byte) ([]byte, error) {
	out, err := mtfseq.Decode(indices, m.alpha)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SelfCheck verifies the four invariants on built-in data with fresh
// instances; it never mutates the receiver.
func (m *MTF) SelfCheck() error {
	alpha := []byte("abcdef")
	for _, data := range [][]byte{{}, []byte("ababaadd"), []byte("fedcba"), []byte("aaaaaa"), []byte("abcdefabcdef")} {
		g, err := New(alpha)
		if err != nil {
			return err
		}
		enc, err := g.Encode(data) // invariant 1: equals mtfseq reference
		if err != nil {
			return err
		}
		if ref, e := mtfseq.Encode(data, alpha); e != nil || !equal(enc, ref) {
			return errors.New("api self-check: encode differs from reference")
		}
		if dec, e := g.Decode(enc); e != nil || !equal(dec, data) { // invariant 2
			return errors.New("api self-check: round trip differs from input")
		}
		h, _ := New(alpha)
		if enc2, _ := h.Encode(data); !equal(enc, enc2) { // invariant 2: deterministic
			return errors.New("api self-check: encode is not deterministic")
		}
	}
	for _, idx := range [][]byte{{}, {0, 1, 1, 1, 1, 0, 3, 0}, {5, 5, 5}} {
		d, _ := New(alpha)
		dec, err := d.Decode(idx)
		if err != nil {
			return err
		}
		if ref, e := mtfseq.Decode(idx, alpha); e != nil || !equal(dec, ref) {
			return errors.New("api self-check: decode differs from reference")
		}
	}
	if err := mtf.SelfCheck(); err != nil { // invariant 3: list/map integrity
		return err
	}
	return checkAtomicFailure(alpha) // invariant 4
}

// checkAtomicFailure verifies a rejected call leaves no trace: the next
// valid output equals that of a brand-new instance.
func checkAtomicFailure(alpha []byte) error {
	bad, _ := New(alpha)
	if _, err := bad.Encode([]byte("abz")); !errors.Is(err, ErrUnknownSymbol) {
		return errors.New("api self-check: missing ErrUnknownSymbol")
	}
	fresh, _ := New(alpha)
	got, _ := bad.Encode([]byte("ab"))
	want, _ := fresh.Encode([]byte("ab"))
	if !equal(got, want) {
		return errors.New("api self-check: state changed after rejected encode")
	}
	if _, err := bad.Decode([]byte{0, 1, 9}); !errors.Is(err, ErrInvalidIndex) {
		return errors.New("api self-check: missing ErrInvalidIndex")
	}
	fresh2, _ := New(alpha)
	got, _ = bad.Decode([]byte{0, 1})
	want, _ = fresh2.Decode([]byte{0, 1})
	if !equal(got, want) {
		return errors.New("api self-check: state changed after rejected decode")
	}
	return nil
}

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
