// Package hcode implements canonical Huffman coding on htree code lengths.
package hcode

import (
	"errors"
	"sort"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrUnknownSymbol  = errors.New("hcode: unknown symbol")
	ErrTruncated      = errors.New("hcode: truncated bit stream")
	ErrIllegalPadding = errors.New("hcode: illegal padding bits")
)

// Table is a canonical Huffman code table, immutable after Build.
// count/first/syml index by code length for O(1) decode lookup.
type Table struct {
	lengths map[byte]int
	codes   map[byte]uint64
	maxLen  int
	count   []int
	first   []uint64
	syml    [][]byte
}

// Build assigns canonical codes: symbols sorted by (length, symbol),
// code shifted left per length step and incremented per symbol.
func Build(lengths map[byte]int) *Table {
	t := &Table{lengths: lengths, codes: map[byte]uint64{}}
	syms := make([]byte, 0, len(lengths))
	for s, l := range lengths {
		syms = append(syms, s)
		if l > t.maxLen {
			t.maxLen = l
		}
	}
	sort.Slice(syms, func(i, j int) bool {
		a, b := syms[i], syms[j]
		return lengths[a] < lengths[b] || lengths[a] == lengths[b] && a < b
	})
	t.count, t.first, t.syml = make([]int, t.maxLen+1), make([]uint64, t.maxLen+1), make([][]byte, t.maxLen+1)
	code, prev := uint64(0), 0
	for _, s := range syms {
		l := lengths[s]
		code <<= uint(l - prev)
		prev = l
		t.codes[s] = code
		if t.count[l] == 0 {
			t.first[l] = code
		}
		t.count[l]++
		t.syml[l] = append(t.syml[l], s)
		code++
	}
	return t
}

// Code returns the canonical code of s and its bit length.
func (t *Table) Code(s byte) (uint64, int, bool) {
	c, ok := t.codes[s]
	return c, t.lengths[s], ok
}

// lookup finds the symbol of a length-l code via the first-code table, O(1).
func (t *Table) lookup(l int, c uint64) (byte, bool) {
	if l <= t.maxLen && t.count[l] > 0 && c >= t.first[l] && c-t.first[l] < uint64(t.count[l]) {
		return t.syml[l][c-t.first[l]], true
	}
	return 0, false
}

// Encode concatenates codewords MSB-first and packs big-endian;
// the last byte is zero-padded on the right.
func (t *Table) Encode(msg []byte) ([]byte, error) {
	out, acc, nbits := []byte(nil), uint64(0), 0
	for _, s := range msg {
		c, ok := t.codes[s]
		if !ok {
			return nil, ErrUnknownSymbol
		}
		acc = acc<<uint(t.lengths[s]) | c
		nbits += t.lengths[s]
		for nbits >= 8 {
			nbits -= 8
			out = append(out, byte(acc>>uint(nbits)))
		}
	}
	if nbits > 0 {
		out = append(out, byte(acc<<uint(8-nbits)))
	}
	return out, nil
}

// Decode decodes exactly n symbols; trailing bits (<8) must be zero.
func (t *Table) Decode(b []byte, n int) ([]byte, error) {
	s := t.NewStream(n)
	s.Feed(b)
	return s.Finish()
}

// Stream is an incremental decoder: bits may arrive in any chunking.
// checks records table entries checked while decoding the last symbol.
type Stream struct {
	t               *Table
	left, curLen    int
	padBits, checks int
	out             []byte
	cur, padVal     uint64
	err             error
}

// NewStream returns a Stream expecting exactly n symbols.
func (t *Table) NewStream(n int) *Stream { return &Stream{t: t, left: n} }

// Feed consumes one chunk of the bit stream.
func (s *Stream) Feed(p []byte) {
	for _, by := range p {
		for i := 7; i >= 0 && s.err == nil; i-- {
			bit := uint64(by) >> uint(i) & 1
			if s.left == 0 { // padding region: bits must stay zero
				s.padBits++
				s.padVal = s.padVal<<1 | bit
				continue
			}
			s.cur, s.curLen = s.cur<<1|bit, s.curLen+1
			if sym, ok := s.t.lookup(s.curLen, s.cur); ok {
				s.checks = s.curLen
				s.out = append(s.out, sym)
				s.left--
				s.cur, s.curLen = 0, 0
			} else if s.curLen >= s.t.maxLen {
				s.err = ErrTruncated
			}
		}
	}
}

// Finish validates the stream end and returns the decoded symbols.
func (s *Stream) Finish() ([]byte, error) {
	switch {
	case s.err != nil:
		return nil, s.err
	case s.left > 0:
		return nil, ErrTruncated
	case s.padBits >= 8 || s.padVal != 0:
		return nil, ErrIllegalPadding
	}
	return s.out, nil
}
