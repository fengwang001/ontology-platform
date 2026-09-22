package segment

import (
	"encoding/binary"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// Dict block layout (all integers big-endian):
//   byte 0       kind: 1=int, 2=str
//   u32          cardinality
//   entries      int: 8 raw int64 bytes each; str: u32 length + bytes
//   byte         code width
//   packed       codes packed at code width (trailing byte zero-padded)

func appendU32(b []byte, x uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], x)
	return append(b, buf[:]...)
}

func appendI64(b []byte, x int64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(x))
	return append(b, buf[:]...)
}

func (g *RowGroup) encodeIntDict(nonNull []zone.Value, d *dict.Dict[int64]) error {
	b := []byte{1}
	b = appendU32(b, uint32(d.Len()))
	for i := 0; i < d.Len(); i++ {
		b = appendI64(b, d.Value(uint64(i)))
	}
	return g.finishDict(b, nonNull, d)
}

func (g *RowGroup) encodeStrDict(nonNull []zone.Value, d *dict.Dict[string]) error {
	b := []byte{2}
	b = appendU32(b, uint32(d.Len()))
	for i := 0; i < d.Len(); i++ {
		s := d.Value(uint64(i))
		b = appendU32(b, uint32(len(s)))
		b = append(b, s...)
	}
	codes := make([]uint64, len(nonNull))
	for i, v := range nonNull {
		codes[i] = d.MustCode(v.S)
	}
	return g.finishDictGeneric(b, codes, uint64(d.Len()))
}

func (g *RowGroup) finishDict(b []byte, nonNull []zone.Value, d *dict.Dict[int64]) error {
	codes := make([]uint64, len(nonNull))
	for i, v := range nonNull {
		codes[i] = d.MustCode(v.I)
	}
	return g.finishDictGeneric(b, codes, uint64(d.Len()))
}

func (g *RowGroup) finishDictGeneric(head []byte, codes []uint64, card uint64) error {
	width := intWidth(card - 1) // codes are 0..card-1; card >= 1
	head = append(head, byte(width))
	head = append(head, make([]byte, bitpack.ByteLen(len(codes), width))...)
	if err := bitpack.Pack(head[len(head)-bitpack.ByteLen(len(codes), width):], codes, width); err != nil {
		return err
	}
	g.data = head
	return nil
}
