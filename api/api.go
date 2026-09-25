// Package api is the public facade of the Shannon-Fano codec.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"ontology/sfano"
	"ontology/sfstream"
)

// Sentinel errors, re-exported so callers can use errors.Is.
var (
	ErrInvalidFreq    = sfano.ErrInvalidFreq
	ErrUnknownSymbol  = sfstream.ErrUnknownSymbol
	ErrTruncated      = sfstream.ErrTruncated
	ErrIllegalPadding = sfstream.ErrIllegalPadding
)

// Codec is the public Shannon-Fano codec. It is read-only after
// construction and safe for concurrent use.
type Codec struct{ sc *sfstream.Codec }

// New validates the frequency table and builds a codec.
func New(freq map[byte]int) (*Codec, error) {
	codes, err := sfano.Build(freq)
	if err != nil {
		return nil, err
	}
	return &Codec{sc: sfstream.New(codes)}, nil
}

// Encode encodes msg, failing wholesale on unknown symbols.
func (c *Codec) Encode(msg []byte) ([]byte, error) { return c.sc.Encode(msg) }

// Decode decodes exactly n symbols, failing wholesale on truncation or
// illegal padding.
func (c *Codec) Decode(b []byte, n int) ([]byte, error) { return c.sc.Decode(b, n) }

// SelfCheck verifies the four invariants on built-in frequencies and
// messages, returning the first violation found.
func SelfCheck() error {
	freq := map[byte]int{'A': 5, 'B': 2, 'C': 1, 'D': 1, 'E': 1}
	codes, err := sfano.Build(freq)
	if err != nil {
		return err
	}
	again, _ := sfano.Build(freq)
	for s, code := range codes { // invariant 3: deterministic + prefix-free
		if again[s] != code {
			return fmt.Errorf("selfcheck: nondeterministic code for %q", s)
		}
		for s2, c2 := range codes {
			if s != s2 && len(c2) > len(code) && c2[:len(code)] == code {
				return fmt.Errorf("selfcheck: %q prefixes %q", code, c2)
			}
		}
	}
	for s, code := range naiveCodes(freq) { // invariant 1: naive reference
		if codes[s] != code {
			return fmt.Errorf("selfcheck: naive mismatch for %q", s)
		}
	}
	c, err := New(freq)
	if err != nil {
		return err
	}
	for _, msg := range [][]byte{[]byte("AABCCD"), []byte("EDCBA"), {}, []byte("A")} {
		enc, err := c.Encode(msg)
		if err != nil {
			return err
		}
		if dec, err := c.Decode(enc, len(msg)); err != nil || !bytes.Equal(dec, msg) {
			return fmt.Errorf("selfcheck: roundtrip failed for %q", msg)
		}
		f := c.sc.NewFeeder() // invariant 2: chunk-agnostic
		for _, b := range enc {
			f.Feed([]byte{b})
		}
		if dec, err := f.Decode(len(msg)); err != nil || !bytes.Equal(dec, msg) {
			return fmt.Errorf("selfcheck: feed mismatch for %q", msg)
		}
	}
	// invariant 4: failures are atomic; the codec stays usable
	if _, err := New(map[byte]int{'A': 0}); !errors.Is(err, ErrInvalidFreq) {
		return fmt.Errorf("selfcheck: want ErrInvalidFreq, got %v", err)
	}
	if _, err := c.Encode([]byte("Z")); !errors.Is(err, ErrUnknownSymbol) {
		return fmt.Errorf("selfcheck: want ErrUnknownSymbol, got %v", err)
	}
	if _, err := c.Decode([]byte{0x2D}, 6); !errors.Is(err, ErrTruncated) {
		return fmt.Errorf("selfcheck: want ErrTruncated, got %v", err)
	}
	if _, err := c.Decode([]byte{0x2D, 0xB9}, 6); !errors.Is(err, ErrIllegalPadding) {
		return fmt.Errorf("selfcheck: want ErrIllegalPadding, got %v", err)
	}
	if enc, _ := c.Encode([]byte("AABCCD")); len(enc) != 2 {
		return fmt.Errorf("selfcheck: codec broken after failures")
	}
	return nil
}

// naiveCodes is a deliberately naive reference: it recomputes both half
// sums at every cut and compares |left-right| directly.
func naiveCodes(freq map[byte]int) map[byte]string {
	type sym struct {
		b byte
		f int
	}
	items := make([]sym, 0, len(freq))
	for b, f := range freq {
		items = append(items, sym{b, f})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].f != items[j].f {
			return items[i].f > items[j].f
		}
		return items[i].b < items[j].b
	})
	codes := map[byte]string{}
	var rec func(g []sym, prefix string)
	rec = func(g []sym, prefix string) {
		if len(g) == 1 {
			codes[g[0].b] = prefix
			return
		}
		total := 0
		for _, x := range g {
			total += x.f
		}
		best, bestDiff, left := 1, total, 0
		for i := 0; i < len(g)-1; i++ {
			left += g[i].f
			d := 2*left - total
			if d < 0 {
				d = -d
			}
			if d < bestDiff {
				bestDiff, best = d, i+1
			}
		}
		rec(g[:best], prefix+"0")
		rec(g[best:], prefix+"1")
	}
	rec(items, "")
	return codes
}
