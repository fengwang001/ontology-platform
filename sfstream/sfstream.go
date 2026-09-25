// Package sfstream implements bit-level Shannon-Fano encoding and
// decoding over a code table built by package sfano.
package sfstream

import (
	"errors"
	"sync/atomic"
)

var (
	// ErrUnknownSymbol: Encode met a symbol absent from the code table.
	ErrUnknownSymbol = errors.New("sfstream: unknown symbol")
	// ErrTruncated: bit stream ended before n symbols were decoded.
	ErrTruncated = errors.New("sfstream: truncated bit stream")
	// ErrIllegalPadding: bits past the n-th symbol are not all zero.
	ErrIllegalPadding = errors.New("sfstream: illegal padding bits")
)

type node struct {
	child [2]*node
	sym   byte
	leaf  bool
}

// Codec is a bit-level encoder/decoder. The code table is read-only
// after construction, so all methods are safe for concurrent use.
type Codec struct {
	codes map[byte]string
	root  *node
	last  atomic.Int64 // nodes visited while decoding the latest symbol
}

// New builds a prefix tree from a sfano code table.
func New(codes map[byte]string) *Codec {
	c := &Codec{codes: codes, root: &node{}}
	for s, code := range codes {
		n := c.root
		for i := 0; i < len(code); i++ {
			b := code[i] - '0'
			if n.child[b] == nil {
				n.child[b] = &node{}
			}
			n = n.child[b]
		}
		n.leaf, n.sym = true, s
	}
	return c
}

// Encode concatenates codewords into a big-endian bit stream, packing
// eight bits per byte and zero-padding the low bits of the last byte.
func (c *Codec) Encode(msg []byte) ([]byte, error) {
	out := make([]byte, 0, len(msg))
	var cur byte
	var nbits int
	for _, s := range msg {
		code, ok := c.codes[s]
		if !ok {
			return nil, ErrUnknownSymbol
		}
		for i := 0; i < len(code); i++ {
			cur = cur<<1 | (code[i] - '0')
			nbits++
			if nbits == 8 {
				out = append(out, cur)
				cur, nbits = 0, 0
			}
		}
	}
	if nbits > 0 {
		out = append(out, cur<<(8-nbits))
	}
	return out, nil
}

// Decode reads exactly n symbols from the bit stream, then requires the
// remaining bits (< 8) to be zero padding.
func (c *Codec) Decode(b []byte, n int) ([]byte, error) {
	out := make([]byte, 0, n)
	total := len(b) * 8
	pos, visited := 0, 0
	cur := c.root
	for len(out) < n {
		if cur.leaf { // single-symbol alphabet: empty codeword
			out = append(out, cur.sym)
			c.last.Store(int64(visited))
			continue
		}
		if pos >= total {
			return nil, ErrTruncated
		}
		bit := (b[pos/8] >> (7 - pos%8)) & 1
		pos++
		cur = cur.child[bit]
		visited++
		if cur == nil {
			return nil, ErrTruncated
		}
		if cur.leaf {
			out = append(out, cur.sym)
			c.last.Store(int64(visited))
			visited, cur = 0, c.root
		}
	}
	if total-pos >= 8 {
		return nil, ErrIllegalPadding
	}
	for ; pos < total; pos++ {
		if (b[pos/8]>>(7-pos%8))&1 != 0 {
			return nil, ErrIllegalPadding
		}
	}
	return out, nil
}

// Feeder accumulates an encoded stream in arbitrary chunks; decoding the
// accumulated buffer is chunk-boundary agnostic.
type Feeder struct {
	c   *Codec
	buf []byte
}

// NewFeeder returns an incremental feeder bound to this codec.
func (c *Codec) NewFeeder() *Feeder { return &Feeder{c: c} }

// Feed appends one chunk of the stream.
func (f *Feeder) Feed(p []byte) { f.buf = append(f.buf, p...) }

// Decode decodes n symbols from everything fed so far.
func (f *Feeder) Decode(n int) ([]byte, error) { return f.c.Decode(f.buf, n) }
