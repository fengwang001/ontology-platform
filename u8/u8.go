package u8

import "ontology/scalar"

// UnitKind 描述一次 UTF-8 判定的结果类型。
type UnitKind int

const (
	UnitIncomplete UnitKind = iota // 合法前缀但数据不足
	UnitScalar                     // 合法标量
	UnitInvalid                    // 一个非法单元
)

// Unit 是从字节流首部判定出的一个单元。
type Unit struct {
	Kind UnitKind
	Size int  // 本次吞掉的字节数（UnitIncomplete 时为已见前缀长度）
	Rune rune // Kind==UnitScalar 时的标量值
}

// leadInfo 给出首字节的期望长度与第二字节合法区间；ok=false 表示首字节即非法。
func leadInfo(b byte) (n int, lo, hi byte, ok bool) {
	switch {
	case b < 0x80:
		return 1, 0, 0, true
	case 0xC2 <= b && b <= 0xDF:
		return 2, 0x80, 0xBF, true
	case b == 0xE0:
		return 3, 0xA0, 0xBF, true
	case 0xE1 <= b && b <= 0xEC:
		return 3, 0x80, 0xBF, true
	case b == 0xED:
		return 3, 0x80, 0x9F, true
	case 0xEE <= b && b <= 0xEF:
		return 3, 0x80, 0xBF, true
	case b == 0xF0:
		return 4, 0x90, 0xBF, true
	case 0xF1 <= b && b <= 0xF3:
		return 4, 0x80, 0xBF, true
	case b == 0xF4:
		return 4, 0x80, 0x8F, true
	}
	return 0, 0, 0, false
}

// Decode 判定 p 首部的一个 UTF-8 单元，不做任何隐式解码。
func Decode(p []byte) Unit {
	if len(p) == 0 {
		return Unit{Kind: UnitIncomplete}
	}
	b0 := p[0]
	n, lo, hi, ok := leadInfo(b0)
	if !ok {
		return Unit{Kind: UnitInvalid, Size: 1}
	}
	if n == 1 {
		return Unit{Kind: UnitScalar, Size: 1, Rune: rune(b0)}
	}
	if len(p) < 2 {
		return Unit{Kind: UnitIncomplete, Size: 1}
	}
	if p[1] < lo || p[1] > hi {
		return Unit{Kind: UnitInvalid, Size: 1}
	}
	for k := 2; k < n; k++ {
		if len(p) <= k {
			return Unit{Kind: UnitIncomplete, Size: k}
		}
		if p[k] < 0x80 || p[k] > 0xBF {
			return Unit{Kind: UnitInvalid, Size: k}
		}
	}
	r := rune(b0) & (0xFF >> (n + 1))
	for k := 1; k < n; k++ {
		r = r<<6 | rune(p[k]&0x3F)
	}
	if !scalar.Valid(r) {
		return Unit{Kind: UnitInvalid, Size: n}
	}
	return Unit{Kind: UnitScalar, Size: n, Rune: r}
}

// EncodeLen 返回标量的 UTF-8 编码字节数。
func EncodeLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode 把标量写入 b（调用方保证容量足够），返回写入字节数。
func Encode(b []byte, r rune) int {
	switch n := EncodeLen(r); n {
	case 1:
		b[0] = byte(r)
	case 2:
		b[0] = 0xC0 | byte(r>>6)
		b[1] = 0x80 | byte(r)&0x3F
	case 3:
		b[0] = 0xE0 | byte(r>>12)
		b[1] = 0x80 | byte(r>>6)&0x3F
		b[2] = 0x80 | byte(r)&0x3F
	default:
		b[0] = 0xF0 | byte(r>>18)
		b[1] = 0x80 | byte(r>>12)&0x3F
		b[2] = 0x80 | byte(r>>6)&0x3F
		b[3] = 0x80 | byte(r)&0x3F
	}
	return EncodeLen(r)
}
