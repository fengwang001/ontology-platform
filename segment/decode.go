package segment

import (
	"encoding/binary"
	"errors"

	"ontology/bitpack"
	"ontology/zone"
)

var errFormat = errors.New("malformed data block")

// Decode reconstructs all rows of the group, including NULL rows at their
// original positions. Every call performs real decoding work.
func (g *RowGroup) Decode() ([]zone.Value, error) {
	vals := make([]zone.Value, g.rows) // zero value is NULL
	if g.nulls == g.rows {
		return vals, nil
	}
	nonNullCount := g.rows - g.nulls
	var nonNull []zone.Value
	var err error
	switch g.enc {
	case EncBitPack:
		nonNull, err = decodeBitPack(g.data, nonNullCount, g.kind)
	case EncDict:
		nonNull, err = decodeDict(g.data, nonNullCount)
	default:
		err = errFormat
	}
	if err != nil {
		return nil, err
	}
	if len(nonNull) != nonNullCount {
		return nil, errFormat
	}
	idx := 0
	for pos := 0; pos < g.rows; pos++ {
		if idx < len(g.nullSet) && g.nullSet[idx] == pos {
			idx++
			continue
		}
		vals[pos] = nonNull[pos-idx]
	}
	return vals, nil
}

func decodeBitPack(data []byte, n int, kind zone.Kind) ([]zone.Value, error) {
	if len(data) < 1 {
		return nil, errFormat
	}
	width := int(data[0])
	if width < 1 || width > 64 || len(data) < 1+bitpack.ByteLen(n, width) {
		return nil, errFormat
	}
	codes, err := bitpack.Unpack(data[1:1+bitpack.ByteLen(n, width)], n, width)
	if err != nil {
		return nil, errFormat
	}
	if kind != zone.Int {
		return nil, errFormat
	}
	out := make([]zone.Value, n)
	for i, u := range codes {
		out[i] = zone.IntValue(unsignedToSigned(u))
	}
	return out, nil
}

type cursor struct {
	b []byte
	p int
}

func (c *cursor) bytes(n int) ([]byte, error) {
	if n < 0 || c.p+n > len(c.b) {
		return nil, errFormat
	}
	r := c.b[c.p : c.p+n]
	c.p += n
	return r, nil
}

func (c *cursor) u32() (uint32, error) {
	b, err := c.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (c *cursor) i64() (int64, error) {
	b, err := c.bytes(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

func decodeDict(data []byte, n int) ([]zone.Value, error) {
	c := &cursor{b: data}
	k, err := c.bytes(1)
	if err != nil {
		return nil, err
	}
	card, err := c.u32()
	if err != nil || card == 0 {
		return nil, errFormat
	}
	switch k[0] {
	case 1:
		entries := make([]int64, card)
		for i := range entries {
			entries[i], err = c.i64()
			if err != nil {
				return nil, err
			}
		}
		return decodeIntCodes(c, n, entries)
	case 2:
		entries := make([]string, card)
		for i := range entries {
			l, e := c.u32()
			if e != nil {
				return nil, e
			}
			s, e := c.bytes(int(l))
			if e != nil {
				return nil, e
			}
			entries[i] = string(s)
		}
		return decodeStrCodes(c, n, entries)
	default:
		return nil, errFormat
	}
}

func decodeIntCodes(c *cursor, n int, entries []int64) ([]zone.Value, error) {
	wb, err := c.bytes(1)
	if err != nil {
		return nil, err
	}
	width := int(wb[0])
	raw, err := c.bytes(bitpack.ByteLen(n, width))
	if err != nil || width < 1 || width > 64 {
		return nil, errFormat
	}
	codes, err := bitpack.Unpack(raw, n, width)
	if err != nil {
		return nil, errFormat
	}
	out := make([]zone.Value, n)
	for i, code := range codes {
		if int(code) >= len(entries) {
			return nil, errFormat
		}
		out[i] = zone.IntValue(entries[code])
	}
	return out, nil
}

func decodeStrCodes(c *cursor, n int, entries []string) ([]zone.Value, error) {
	wb, err := c.bytes(1)
	if err != nil {
		return nil, err
	}
	width := int(wb[0])
	raw, err := c.bytes(bitpack.ByteLen(n, width))
	if err != nil || width < 1 || width > 64 {
		return nil, errFormat
	}
	codes, err := bitpack.Unpack(raw, n, width)
	if err != nil {
		return nil, errFormat
	}
	out := make([]zone.Value, n)
	for i, code := range codes {
		if int(code) >= len(entries) {
			return nil, errFormat
		}
		out[i] = zone.StringValue(entries[code])
	}
	return out, nil
}
