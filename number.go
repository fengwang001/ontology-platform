package ontology

import (
	"math"
	"math/bits"
)

// Numbers are encoded as a sign class tag followed by an
// order-preserving magnitude:
//
//	exp2     2-byte big-endian of (binaryExponent + 1074), so the
//	         exponent range [-1074, 1024] maps to [0, 2098] and byte
//	         order equals exponent order.
//	mantissa big-endian fraction bits (value = 1.fraction x 2^exp),
//	         left-aligned, trailing zero bytes trimmed, 0x00 escaped
//	         as 0x00 0xFF, terminated by 0x00 0x00.
//	type     typeInt or typeFloat; breaks numeric ties and preserves
//	         the Go type. Not complemented for negative numbers.
//
// Negative numbers complement exp2+mantissa of the magnitude, so
// larger magnitude (more negative) sorts first. Small integers need
// at most 1 mantissa byte, so |v| < 128 encodes in 6 bytes total.

const expBias = 1074

func appendInt(out []byte, v int64) []byte {
	switch {
	case v == 0:
		return append(out, tagZero, typeInt)
	case v > 0:
		out = append(out, tagPos)
		return appendMag(out, decomposeInt(uint64(v)), typeInt, false)
	default:
		out = append(out, tagNeg)
		mag := uint64(-(v + 1)) + 1 // safe for math.MinInt64
		return appendMag(out, decomposeInt(mag), typeInt, true)
	}
}

func appendFloat(out []byte, f float64, nanCount *int) []byte {
	switch {
	case math.IsNaN(f):
		*nanCount++
		return append(out, tagNil)
	case f == 0: // covers -0.0, which compares equal to 0.0
		return append(out, tagZero, typeFloat)
	case f > 0:
		out = append(out, tagPos)
		return appendMag(out, decomposeFloat(f), typeFloat, false)
	default:
		out = append(out, tagNeg)
		return appendMag(out, decomposeFloat(-f), typeFloat, true)
	}
}

// magnitude is a left-aligned fraction: value = (1 + frac/2^64scale)
// is implied by exp; frac holds the fraction bits left-aligned in a
// uint64, trailing zero bytes trimmed by mantissaBytes.
type magnitude struct {
	exp  int
	frac uint64 // fraction bits, left-aligned
}

func decomposeInt(mag uint64) magnitude {
	e := 63 - bits.LeadingZeros64(mag)
	frac := mag - (1 << uint(e)) // e significant bits
	return magnitude{exp: e, frac: frac << uint(64-e)}
}

func decomposeFloat(f float64) magnitude {
	b := math.Float64bits(f)
	rawExp := int(b>>52) & 0x7FF
	fracBits := b & (uint64(1)<<52 - 1)
	if rawExp == 0 { // subnormal: normalize so the top bit sits at 52
		bl := 64 - bits.LeadingZeros64(fracBits)
		shift := 53 - bl
		mant := fracBits << uint(shift)
		return magnitude{exp: -1022 - shift, frac: (mant & (uint64(1)<<52 - 1)) << 12}
	}
	mant := fracBits | (uint64(1) << 52)
	return magnitude{exp: rawExp - 1023, frac: (mant & (uint64(1)<<52 - 1)) << 12}
}

func mantissaBytes(frac uint64) []byte {
	var buf [8]byte
	for i := 7; i >= 0; i-- {
		buf[i] = byte(frac)
		frac >>= 8
	}
	n := 8
	for n > 0 && buf[n-1] == 0 {
		n--
	}
	return buf[:n]
}

func appendMag(out []byte, m magnitude, typeByte byte, neg bool) []byte {
	u := uint16(m.exp + expBias)
	body := []byte{byte(u >> 8), byte(u)}
	for _, b := range mantissaBytes(m.frac) {
		if b == termByte {
			body = append(body, termByte, escapeByte)
		} else {
			body = append(body, b)
		}
	}
	body = append(body, termByte, termByte)
	if neg {
		for i := range body {
			body[i] = ^body[i]
		}
	}
	out = append(out, body...)
	return append(out, typeByte)
}

// recomposeInt rebuilds a magnitude as uint64 from exponent and
// left-aligned fraction bytes.
func recomposeInt(exp int, mant []byte) uint64 {
	frac := alignFrac(mant) >> uint(64-exp)
	return (uint64(1) << uint(exp)) + frac
}

// recomposeFloat rebuilds a positive float64 magnitude.
func recomposeFloat(exp int, mant []byte) float64 {
	mant53 := (uint64(1) << 52) | (alignFrac(mant) >> 12)
	var b uint64
	if exp >= -1022 {
		b = uint64(exp+1023)<<52 | (mant53 & (uint64(1)<<52 - 1))
	} else {
		b = mant53 >> uint(-1022-exp)
	}
	return math.Float64frombits(b)
}

func alignFrac(mant []byte) uint64 {
	var frac uint64
	for i, b := range mant {
		frac |= uint64(b) << uint(56-8*i)
	}
	return frac
}
