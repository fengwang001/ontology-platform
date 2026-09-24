// Package codec is the public entry point for signed-int varint codec. Slices
// use END-delimited SLIP framing: a prefix/suffix cut at any byte rejoins to
// the same result or alone yields ErrIncomplete, never wrong values.
package codec

import (
	"errors"
	"fmt"
	"math"

	"ontology/vint"
	"ontology/zz"
)

const (
	end, esc     byte = 0xC0, 0xDB
	escE, escF   byte = 0xDC, 0xDD
)

var (
	ErrIncomplete = errors.New("codec: incomplete input")
	ErrCorrupt    = errors.New("codec: corrupt input")
	ErrOverflow   = errors.New("codec: value overflows")
)

// Codec is safe for concurrent use.
type Codec struct{ dec *vint.Decoder }

func New() *Codec { return &Codec{dec: vint.NewDecoder()} }

func mapErr(e error) error {
	switch {
	case errors.Is(e, vint.ErrIncomplete):
		return ErrIncomplete
	case errors.Is(e, vint.ErrOverflow):
		return ErrOverflow
	default:
		return ErrCorrupt
	}
}

// EncodeInt encodes one signed integer; DecodeInt rejects trailing bytes.
func (c *Codec) EncodeInt(v int64) []byte { return vint.PutUvarint(nil, zz.Zig(v)) }

func (c *Codec) DecodeInt(buf []byte) (int64, error) {
	u, n, err := c.dec.Uvarint(buf)
	if err != nil {
		return 0, mapErr(err)
	}
	if n != len(buf) {
		return 0, fmt.Errorf("%w: trailing bytes", ErrCorrupt)
	}
	return zz.Unzig(u), nil
}

func stuff(b []byte, x byte) []byte {
	switch x {
	case end:
		return append(b, esc, escE)
	case esc:
		return append(b, esc, escF)
	default:
		return append(b, x)
	}
}

// EncodeSlice emits END [escaped count+elements] END.
func (c *Codec) EncodeSlice(vs []int64) []byte {
	raw := vint.PutUvarint(nil, uint64(len(vs)))
	for _, v := range vs {
		raw = vint.PutUvarint(raw, zz.Zig(v))
	}
	b := []byte{end}
	for _, x := range raw {
		b = stuff(b, x)
	}
	return append(b, end)
}

// Stream reassembles bytes fed in any chunking.
type Stream struct {
	c       *Codec
	payload []byte
	inFrame bool
	pending bool
}

func (c *Codec) NewStream() *Stream { return &Stream{c: c} }

// Feed returns the slice only when a complete frame arrives.
func (s *Stream) Feed(chunk []byte) ([]int64, error) {
	for i := 0; i < len(chunk); i++ {
		x := chunk[i]
		switch {
		case s.pending:
			s.pending = false
			switch x {
			case escE:
				s.payload = append(s.payload, end)
			case escF:
				s.payload = append(s.payload, esc)
			default:
				return nil, fmt.Errorf("%w: bad escape", ErrCorrupt)
			}
		case x == end:
			if s.inFrame {
				fr := s.payload
				s.payload, s.inFrame = nil, false
				return s.parse(fr)
			}
			s.inFrame, s.payload = true, s.payload[:0]
		case !s.inFrame:
		case x == esc:
			s.pending = true
		default:
			s.payload = append(s.payload, x)
		}
	}
	return nil, ErrIncomplete
}

// DecodeSlice decodes one complete frame or returns a named failure.
func (c *Codec) DecodeSlice(buf []byte) ([]int64, error) {
	return c.NewStream().Feed(buf)
}

func (s *Stream) parse(frame []byte) ([]int64, error) {
	cnt, n, err := s.c.dec.Uvarint(frame)
	if err != nil {
		return nil, wrap("count", math.MaxUint64, err)
	}
	out, p := make([]int64, 0, cnt), n
	for i := uint64(0); i < cnt; i++ {
		u, m, err := s.c.dec.Uvarint(frame[p:])
		if err != nil {
			return nil, wrap("element", i, err)
		}
		out, p = append(out, zz.Unzig(u)), p+m
	}
	if p != len(frame) {
		return nil, fmt.Errorf("codec: element %d: %w (trailing)", cnt, ErrCorrupt)
	}
	return out, nil
}

func wrap(kind string, idx uint64, err error) error {
	if errors.Is(err, vint.ErrIncomplete) {
		return ErrIncomplete
	}
	return fmt.Errorf("codec: %s %d: %w", kind, idx, mapErr(err))
}

// SelfCheck round-trips fixed values and verifies the 10-byte length bound.
func (c *Codec) SelfCheck() error {
	for _, v := range []int64{0, -1, 1, -2, math.MinInt64, math.MaxInt64} {
		b := c.EncodeInt(v)
		if len(b) > 10 {
			return fmt.Errorf("selfcheck: %d uses %d bytes", v, len(b))
		}
		if got, err := c.DecodeInt(b); err != nil || got != v {
			return fmt.Errorf("selfcheck: roundtrip %d got %d err %v", v, got, err)
		}
	}
	return nil
}
