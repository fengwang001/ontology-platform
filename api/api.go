// Package api is the public face: nibble slices to byte streams with
// Hamming(7,4) forward error correction, and back with correction.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/hamm"
	"ontology/hpack"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidNibble  = hamm.ErrInvalidNibble
	ErrTruncated      = hpack.ErrTruncated
	ErrIllegalPadding = hpack.ErrIllegalPadding
)

// Encode encodes nibbles (each 0..15) into a packed byte stream.
// Any out-of-range nibble fails the whole call with ErrInvalidNibble.
func Encode(ns []int) ([]byte, error) {
	words := make([]uint8, len(ns))
	for i, n := range ns {
		w, err := hamm.EncodeNibble(n)
		if err != nil {
			return nil, err
		}
		words[i] = w
	}
	return hpack.Pack(words), nil
}

// Decode unpacks n codewords from b, corrects single-bit errors and
// returns the nibbles. Any failure returns nil, never partial results.
func Decode(b []byte, n int) ([]int, error) {
	words, err := hpack.Unpack(b, n)
	if err != nil {
		return nil, err
	}
	out := make([]int, n)
	for i, w := range words {
		out[i] = hamm.DecodeNibble(w)
	}
	return out, nil
}

// naiveEncode is the deliberately simple reference: per-nibble parity by
// the defining formulas, big-endian bit append one bit at a time.
func naiveEncode(ns []int) []byte {
	var bits []int
	for _, n := range ns {
		d := []int{n >> 3 & 1, n >> 2 & 1, n >> 1 & 1, n & 1}
		bits = append(bits, d[0]^d[1]^d[3], d[0]^d[2]^d[3], d[0],
			d[1]^d[2]^d[3], d[1], d[2], d[3])
	}
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b == 1 {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

// SelfCheck verifies the four invariants on built-in sequences.
func SelfCheck() error {
	seqs := [][]int{{}, {0}, {11}, {15, 0, 7, 8}, {1, 2, 4, 8, 15, 9, 3}}
	for _, ns := range seqs {
		enc, err := Encode(ns)
		if err != nil || !bytes.Equal(enc, naiveEncode(ns)) {
			return fmt.Errorf("selfcheck: encode mismatch %v", ns)
		}
		if enc2, _ := Encode(ns); !bytes.Equal(enc, enc2) {
			return fmt.Errorf("selfcheck: nondeterministic %v", ns)
		}
		dec, err := Decode(enc, len(ns))
		if err != nil || !equal(dec, ns) {
			return fmt.Errorf("selfcheck: roundtrip %v", ns)
		}
		for bit := 0; bit < 7*len(ns); bit++ { // every single-bit error
			c := bytes.Clone(enc)
			c[bit/8] ^= 1 << (7 - bit%8)
			if got, err := Decode(c, len(ns)); err != nil || !equal(got, ns) {
				return fmt.Errorf("selfcheck: single error bit %d of %v", bit, ns)
			}
		}
		var f hpack.Feeder // chunk boundaries must not matter
		for _, b := range enc {
			f.Feed([]byte{b})
		}
		if got, err := Decode(f.Bytes(), len(ns)); err != nil || !equal(got, ns) {
			return fmt.Errorf("selfcheck: chunked feed %v", ns)
		}
	}
	if got, err := Decode([]byte{0xff}, 2); !errors.Is(err, ErrTruncated) || got != nil {
		return fmt.Errorf("selfcheck: truncation not wholesale")
	}
	if got, err := Decode([]byte{0x67}, 1); !errors.Is(err, ErrIllegalPadding) || got != nil {
		return fmt.Errorf("selfcheck: padding not wholesale")
	}
	if _, err := Encode([]int{16}); !errors.Is(err, ErrInvalidNibble) {
		return fmt.Errorf("selfcheck: invalid nibble")
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
