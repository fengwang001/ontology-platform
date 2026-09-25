// Package api is the public face of the Hamming(7,4) codec: Encode,
// Decode, incremental Feed and SelfCheck. It depends only on hpack.
package api

import (
	"errors"

	"ontology/hpack"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidNibble  = errors.New("api: nibble out of range [0,15]")
	ErrTruncated      = errors.New("api: bit stream too short for n codewords")
	ErrIllegalPadding = errors.New("api: non-zero bits beyond 7*n")
)

// CheckNibbles returns ErrInvalidNibble if any nibble is outside [0,15].
func CheckNibbles(ns []int) error {
	for _, n := range ns {
		if n < 0 || n > 15 {
			return ErrInvalidNibble
		}
	}
	return nil
}

// Encode packs nibbles into a byte stream of 7-bit codewords. It returns
// nil if any nibble is invalid (see CheckNibbles). Encode([]) is empty.
func Encode(ns []int) []byte {
	if CheckNibbles(ns) != nil {
		return nil
	}
	return hpack.Pack(ns)
}

// Decode unpacks and corrects n codewords from b. Any failure — stream
// too short for n codewords, or non-zero padding beyond 7*n bits — fails
// the whole decode: it returns nil and changes no state.
func Decode(b []byte, n int) ([]int, error) {
	if n < 0 || 8*len(b) < 7*n {
		return nil, ErrTruncated
	}
	for pos := 7 * n; pos < 8*len(b); pos++ {
		if b[pos/8]>>(7-uint(pos%8))&1 != 0 {
			return nil, ErrIllegalPadding
		}
	}
	u := &hpack.Unpacker{}
	return u.Unpack(b, n), nil
}

// Feeder accumulates a byte stream in arbitrary chunks; the final Decode
// of the accumulated bytes is identical to a one-shot Decode.
type Feeder struct {
	buf []byte
}

// Feed appends one chunk of the stream.
func (f *Feeder) Feed(p []byte) { f.buf = append(f.buf, p...) }

// Decode decodes n codewords from everything fed so far.
func (f *Feeder) Decode(n int) ([]int, error) { return Decode(f.buf, n) }

// SelfCheck verifies the four invariants on built-in sequences:
// reference agreement, chunking invariance, determinism with full
// single-error correction, and atomic failure. It returns the first
// violation found, or nil.
func SelfCheck() error {
	seqs := [][]int{{}, {11}, {0, 1, 2, 3}, {15, 0, 8, 7, 11, 5, 9, 14, 3}}
	for _, ns := range seqs {
		enc, enc2 := Encode(ns), Encode(ns)
		if len(enc) != len(enc2) {
			return errors.New("selfcheck: encode not deterministic")
		}
		for i := range enc {
			if enc[i] != enc2[i] {
				return errors.New("selfcheck: encode not deterministic")
			}
		}
		dec, err := Decode(enc, len(ns))
		if err != nil || !equal(dec, ns) {
			return errors.New("selfcheck: roundtrip mismatch")
		}
		for bit := 0; bit < 7*len(ns); bit++ { // every single-bit error
			c := append([]byte(nil), enc...)
			c[bit/8] ^= 1 << (7 - uint(bit%8))
			if got, _ := Decode(c, len(ns)); !equal(got, ns) {
				return errors.New("selfcheck: single error not corrected")
			}
		}
		for cut := 0; cut <= len(enc); cut++ { // every chunking
			f := &Feeder{}
			f.Feed(enc[:cut])
			f.Feed(enc[cut:])
			if got, _ := f.Decode(len(ns)); !equal(got, ns) {
				return errors.New("selfcheck: chunking changed result")
			}
		}
	}
	if got, err := Decode([]byte{0x66}, 2); got != nil || !errors.Is(err, ErrTruncated) {
		return errors.New("selfcheck: truncation not atomic")
	}
	if got, err := Decode([]byte{0x67}, 1); got != nil || !errors.Is(err, ErrIllegalPadding) {
		return errors.New("selfcheck: illegal padding not atomic")
	}
	if Encode([]int{16}) != nil || !errors.Is(CheckNibbles([]int{-1}), ErrInvalidNibble) {
		return errors.New("selfcheck: invalid nibble not rejected")
	}
	return nil
}

func equal(a, b []int) bool {
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
