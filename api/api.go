// Package api is the public face of the Golomb/Rice coder.
// It depends only on ontology/gstream.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/gstream"
)

// Mutually distinct sentinel errors, re-exported for callers.
var (
	ErrInvalidParam = gstream.ErrInvalidParam
	ErrNegative     = gstream.ErrNegative
	ErrTruncated    = gstream.ErrTruncated
)

// Coder is an immutable Golomb coder for a fixed M; all methods are pure
// and safe for concurrent use.
type Coder struct {
	m int
}

// New returns a coder for M; M <= 0 is rejected with ErrInvalidParam.
func New(M int) (*Coder, error) {
	if _, err := gstream.NewDecoder(M); err != nil {
		return nil, err
	}
	return &Coder{m: M}, nil
}

// Encode encodes non-negative values into a byte stream.
func (c *Coder) Encode(vs []int64) ([]byte, error) {
	return gstream.Encode(c.m, vs)
}

// Decode decodes a stream; truncation fails wholesale with ErrTruncated.
func (c *Coder) Decode(p []byte) ([]int64, error) {
	return gstream.Decode(c.m, p)
}

// SelfCheck verifies the four invariants on built-in sequences and returns
// the first violation, or nil. Sequences never end in 0: a trailing zero
// value is an all-zero code word, indistinguishable from zero padding.
func SelfCheck() error {
	seqs := map[int][][]int64{
		1: {{1}, {3, 0, 2}, {7, 6, 5, 4, 3, 2, 1}},
		2: {{0}, {1, 2, 3}, {100, 0, 255, 1}},
		5: {{0, 2, 4, 5, 9}, {0}, {42, 7, 13}},
		8: {{0}, {1, 8, 64, 1000}},
	}
	for m, cases := range seqs {
		c, err := New(m)
		if err != nil {
			return err
		}
		for _, vs := range cases {
			enc1, err := c.Encode(vs)
			if err != nil {
				return err
			}
			enc2, _ := c.Encode(vs)
			if !bytes.Equal(enc1, enc2) { // invariant 3: determinism
				return fmt.Errorf("selfcheck: encode not deterministic for M=%d %v", m, vs)
			}
			dec, err := c.Decode(enc1)
			if err != nil || fmt.Sprint(dec) != fmt.Sprint(vs) { // invariant 1: roundtrip
				return fmt.Errorf("selfcheck: roundtrip mismatch for M=%d %v: %v %v", m, vs, dec, err)
			}
			d, _ := gstream.NewDecoder(m) // invariant 2: chunk-independence
			for _, b := range enc1 {
				d.Feed([]byte{b})
			}
			dec2, err := d.Decode()
			if err != nil || fmt.Sprint(dec2) != fmt.Sprint(vs) {
				return fmt.Errorf("selfcheck: chunked decode mismatch for M=%d %v", m, vs)
			}
		}
		trunc := []byte{0xFF, 0xFF} // unary code never terminates
		if out, err := c.Decode(trunc); !errors.Is(err, ErrTruncated) || out != nil {
			return fmt.Errorf("selfcheck: truncation left partial output for M=%d", m) // invariant 4
		}
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidParam) {
		return fmt.Errorf("selfcheck: M=0 not rejected")
	}
	c, _ := New(5)
	if _, err := c.Encode([]int64{-1}); !errors.Is(err, ErrNegative) {
		return fmt.Errorf("selfcheck: negative value not rejected")
	}
	return nil
}
