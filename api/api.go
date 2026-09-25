// Package api is the public entry point for canonical Huffman coding.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/hcode"
	"ontology/htree"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidFreq    = htree.ErrInvalidFreq
	ErrUnknownSymbol  = hcode.ErrUnknownSymbol
	ErrTruncated      = hcode.ErrTruncated
	ErrIllegalPadding = hcode.ErrIllegalPadding
)

// Codec is an immutable canonical Huffman codec; safe for concurrent use.
type Codec struct{ tab *hcode.Table }

// New builds a Codec from symbol frequencies.
func New(freq map[byte]int) (*Codec, error) {
	lengths, err := htree.Lengths(freq)
	if err != nil {
		return nil, err
	}
	return &Codec{tab: hcode.Build(lengths)}, nil
}

// Encode encodes msg into a big-endian packed byte string.
func (c *Codec) Encode(msg []byte) ([]byte, error) { return c.tab.Encode(msg) }

// Decode decodes exactly n symbols from b.
func (c *Codec) Decode(b []byte, n int) ([]byte, error) { return c.tab.Decode(b, n) }

// NewStream returns an incremental decoder expecting n symbols.
func (c *Codec) NewStream(n int) *hcode.Stream { return c.tab.NewStream(n) }

// CodeString returns the canonical codeword of s as a bit string.
func (c *Codec) CodeString(s byte) (string, bool) {
	code, l, ok := c.tab.Code(s)
	if !ok {
		return "", false
	}
	bits := make([]byte, l)
	for i := l - 1; i >= 0; i-- {
		bits[i] = '0' + byte(code&1)
		code >>= 1
	}
	return string(bits), true
}

// SelfCheck verifies the four invariants on built-in frequencies
// and messages; it returns the first violation found.
func SelfCheck() error {
	freq := map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}
	c, err := New(freq)
	if err != nil {
		return err
	}
	msgs := [][]byte{[]byte("AABCCD"), {}, []byte("E"), []byte("ABCDEABCDE")}
	for _, m := range msgs { // invariant 1: round-trip
		enc, err := c.Encode(m)
		if err != nil {
			return err
		}
		dec, err := c.Decode(enc, len(m))
		if err != nil || !bytes.Equal(dec, m) {
			return fmt.Errorf("selfcheck: round-trip mismatch")
		}
		for _, cut := range []int{0, 1, len(enc) / 2, len(enc)} { // invariant 2
			if cut > len(enc) {
				cut = len(enc)
			}
			st := c.NewStream(len(m))
			st.Feed(enc[:cut])
			st.Feed(enc[cut:])
			got, err := st.Finish()
			if err != nil || !bytes.Equal(got, m) {
				return fmt.Errorf("selfcheck: chunking mismatch")
			}
		}
	}
	c2, _ := New(freq) // invariant 3: determinism
	for s := range freq {
		a, _ := c.CodeString(s)
		b, _ := c2.CodeString(s)
		if a != b {
			return fmt.Errorf("selfcheck: non-deterministic codes")
		}
	}
	if _, err := c.Decode([]byte{0x25}, 6); !errors.Is(err, ErrTruncated) { // invariant 4
		return fmt.Errorf("selfcheck: want ErrTruncated, got %v", err)
	}
	if _, err := c.Encode([]byte("ZZZ")); !errors.Is(err, ErrUnknownSymbol) {
		return fmt.Errorf("selfcheck: want ErrUnknownSymbol, got %v", err)
	}
	if _, err := New(map[byte]int{'A': 0}); !errors.Is(err, ErrInvalidFreq) {
		return fmt.Errorf("selfcheck: want ErrInvalidFreq, got %v", err)
	}
	if _, err := c.Decode([]byte{0x25, 0xB9}, 6); !errors.Is(err, ErrIllegalPadding) {
		return fmt.Errorf("selfcheck: want ErrIllegalPadding, got %v", err)
	}
	return nil
}
