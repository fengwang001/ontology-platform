// Package fcode implements Fibonacci (Zeckendorf) code words and big-endian bit packing. It depends only on fibseq.
package fcode

import (
	"errors"

	"ontology/fibseq"
)

var (
	ErrTruncated   = errors.New("fcode: truncated code word (no terminating 11)")
	ErrOverflow    = fibseq.ErrOverflow
	ErrNotPositive = fibseq.ErrNotPositive
)

// bitWriter packs MSB-first bits into big-endian bytes.
type bitWriter struct {
	buf  []byte
	cur  byte
	used uint8 // bits already placed in cur, 0..7
}

func (w *bitWriter) writeBit(b bool) {
	if b {
		w.cur |= 1 << (7 - w.used)
	}
	if w.used++; w.used == 8 {
		w.buf, w.cur, w.used = append(w.buf, w.cur), 0, 0
	}
}

// EncodeOne returns the word of n as MSB-first bits b1..bk plus separator 1.
func EncodeOne(n int64) ([]bool, error) {
	idx, err := fibseq.Zeckendorf(n)
	if err != nil {
		return nil, err
	}
	k := idx[len(idx)-1]
	bits := make([]bool, k+1)
	for _, i := range idx {
		bits[i-1] = true
	}
	bits[k] = true // separator: word ends in 11 and contains no earlier 11
	return bits, nil
}

// Encode packs words big-endian; unused low bits of the last byte are zero.
func Encode(vs []int64) ([]byte, error) {
	var w bitWriter
	for _, n := range vs {
		bits, err := EncodeOne(n)
		if err != nil {
			return nil, err
		}
		for _, b := range bits {
			w.writeBit(b)
		}
	}
	if w.used == 0 {
		if len(w.buf) == 0 {
			return []byte{}, nil
		}
		return w.buf, nil
	}
	return append(w.buf, w.cur), nil
}

// DecodeOne decodes one word at the start of bits. The returned count is both
// the bits consumed and the bits examined to locate the ending 11; scanning
// starts at this word's own first bit, so it never depends on earlier words.
func DecodeOne(bits []bool) (n int64, consumed int, err error) {
	for i := 1; i < len(bits); i++ {
		if bits[i-1] && bits[i] { // first 11: i-1 is b_k, i the separator
			idx := make([]int, 0, 1)
			for j := 0; j < i; j++ {
				if bits[j] {
					idx = append(idx, j+1)
				}
			}
			n, err = fibseq.SumIndices(idx)
			return n, i + 1, err
		}
	}
	return 0, 0, ErrTruncated
}

// StreamDecoder decodes a packed stream fed in arbitrary byte-sized chunks.
type StreamDecoder struct {
	pending       []bool // bits not yet consumed by a completed code word
	out           []int64
	lastCheckBits int // unexported: locate-11 cost of the latest completed word
}

func byteBits(p []byte) []bool {
	bits := make([]bool, 0, len(p)*8)
	for _, b := range p {
		for j := 7; j >= 0; j-- {
			bits = append(bits, b&(1<<j) != 0)
		}
	}
	return bits
}

// Feed consumes one chunk and returns the values completed by it. On error it
// commits nothing, so the decoder keeps its prior state and stays usable.
func (s *StreamDecoder) Feed(p []byte) (got []int64, err error) {
	trial := append(append([]bool{}, s.pending...), byteBits(p)...)
	off, checked := 0, s.lastCheckBits
	for {
		n, used, e := DecodeOne(trial[off:])
		if e != nil {
			if !errors.Is(e, ErrTruncated) {
				return nil, e // overflow: commit nothing, no field changes
			}
			break
		}
		checked, got, off = used, append(got, n), off+used // this word's length only
	}
	s.lastCheckBits = checked
	s.pending = append(s.pending[:0], trial[off:]...)
	s.out = append(s.out, got...)
	return got, nil
}

// End finishes the stream: an all-zero trailing suffix is padding; a suffix
// still containing a 1 without 11 is ErrTruncated.
func (s *StreamDecoder) End() ([]int64, error) {
	for _, b := range s.pending {
		if b {
			return nil, ErrTruncated
		}
	}
	out := s.out
	*s = StreamDecoder{}
	if out == nil {
		out = []int64{}
	}
	return out, nil
}

// Decode decodes one complete packed stream, returning nil on any error.
func Decode(p []byte) ([]int64, error) {
	var sd StreamDecoder
	if _, e := sd.Feed(p); e != nil {
		return nil, e
	}
	return sd.End()
}
