package u16

import "ontology/scalar"

type Status int

const (
	Incomplete Status = iota
	OK
	Invalid
)

// Unit 描述一次解码；Size 为吞掉的字节数（2 或 4）。
type Unit struct {
	Status Status
	R      rune
	Size   int
}

func step(p []byte, big bool) Unit {
	if len(p) == 0 {
		return Unit{Status: Incomplete}
	}
	if len(p) < 2 {
		return Unit{Status: Incomplete, Size: 1}
	}
	var u uint16
	if big {
		u = uint16(p[0])<<8 | uint16(p[1])
	} else {
		u = uint16(p[1])<<8 | uint16(p[0])
	}
	switch {
	case scalar.IsHighSurrogate(u):
		if len(p) < 4 {
			return Unit{Status: Incomplete, Size: len(p)}
		}
		var l uint16
		if big {
			l = uint16(p[2])<<8 | uint16(p[3])
		} else {
			l = uint16(p[3])<<8 | uint16(p[2])
		}
		if !scalar.IsLowSurrogate(l) {
			// 高代理后的非低代理重新当作新字符。
			return Unit{Status: Invalid, Size: 2}
		}
		return Unit{Status: OK, R: scalar.SurrogatePair(u, l), Size: 4}
	case scalar.IsLowSurrogate(u):
		return Unit{Status: Invalid, Size: 2}
	default:
		return Unit{Status: OK, R: rune(u), Size: 2}
	}
}

func StepLE(p []byte) Unit { return step(p, false) }
func StepBE(p []byte) Unit { return step(p, true) }

func encPair(buf []byte, big bool, r rune) []byte {
	x := uint32(r) - 0x10000
	hi := uint16(0xD800 + x>>10)
	lo := uint16(0xDC00 + x&0x3FF)
	put := func(u uint16) {
		if big {
			buf = append(buf, byte(u>>8), byte(u))
		} else {
			buf = append(buf, byte(u), byte(u>>8))
		}
	}
	put(hi)
	put(lo)
	return buf
}

func appendEncode(buf []byte, big bool, r rune) []byte {
	if r >= 0x10000 {
		return encPair(buf, big, r)
	}
	u := uint16(r)
	if big {
		return append(buf, byte(u>>8), byte(u))
	}
	return append(buf, byte(u), byte(u>>8))
}

func AppendEncodeLE(buf []byte, r rune) []byte { return appendEncode(buf, false, r) }
func AppendEncodeBE(buf []byte, r rune) []byte { return appendEncode(buf, true, r) }
