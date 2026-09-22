package segment

import (
	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// intWidth returns the smallest bit width covering all non-negative codes,
// which is 1 for a single code (code 0).
func intWidth(maxCode uint64) int {
	for w := 1; w < 64; w++ {
		if maxCode < uint64(1)<<uint(w) {
			return w
		}
	}
	return 64
}

func buildIntDict(vals []zone.Value, maxCard int) (*dict.Dict[int64], error) {
	keys := make([]int64, len(vals))
	for i, v := range vals {
		keys[i] = v.I
	}
	return dict.Build(keys, maxCard)
}

func buildStrDict(vals []zone.Value, maxCard int) (*dict.Dict[string], error) {
	keys := make([]string, len(vals))
	for i, v := range vals {
		keys[i] = v.S
	}
	return dict.Build(keys, maxCard)
}

func (g *RowGroup) encode(nonNull []zone.Value, force Encoding, maxCard int) error {
	if len(nonNull) == 0 {
		g.enc = EncBitPack
		g.data = nil
		return nil
	}
	g.enc = force
	if g.enc == 0 {
		if g.kind == zone.Str {
			g.enc = EncDict
		} else {
			g.enc = EncDict // try dict; downgrade below if too large
		}
	}
	switch g.enc {
	case EncBitPack:
		return g.encodeBitPack(nonNull)
	case EncDict:
		if g.kind == zone.Int {
			d, err := buildIntDict(nonNull, maxCard)
			if err == dict.ErrTooMany {
				g.enc = EncBitPack // automatic downgrade path
				return g.encodeBitPack(nonNull)
			}
			if err != nil {
				return err
			}
			return g.encodeIntDict(nonNull, d)
		}
		d, err := buildStrDict(nonNull, maxCard)
		if err != nil {
			return err
		}
		return g.encodeStrDict(nonNull, d)
	}
	return nil
}

// signedToUnsigned maps int64 bijectively to uint64 by flipping the sign bit,
// preserving ordering so width can be based on the maximum.
func signedToUnsigned(i int64) uint64 { return uint64(i) ^ 1<<63 }

func unsignedToSigned(u uint64) int64 { return int64(u ^ 1<<63) }

func (g *RowGroup) encodeBitPack(nonNull []zone.Value) error {
	uvals := make([]uint64, len(nonNull))
	var max uint64
	for i, v := range nonNull {
		u := signedToUnsigned(v.I)
		uvals[i] = u
		if u > max {
			max = u
		}
	}
	width := intWidth(max)
	buf := make([]byte, 1+bitpack.ByteLen(len(uvals), width))
	buf[0] = byte(width)
	if err := bitpack.Pack(buf[1:], uvals, width); err != nil {
		return err
	}
	g.data = buf
	return nil
}
