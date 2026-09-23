package u16

import "ontology/scalar"

// Order 表示 UTF-16 字节序。
type Order int

const (
	LittleEndian Order = iota
	BigEndian
)

// UnitKind 描述一次 UTF-16 判定的结果类型。
type UnitKind int

const (
	UnitIncomplete UnitKind = iota // 数据不足：奇数字节或高代理缺后半个
	UnitScalar                     // 合法标量（含代理对合成）
	UnitLoneHigh                   // 孤立高代理（含高代理后跟非低代理）
	UnitLoneLow                    // 孤立低代理
)

// Unit 是从字节流首部判定出的一个 UTF-16 单元。
type Unit struct {
	Kind UnitKind
	Size int  // 吞掉的字节数
	Rune rune
}

func read16(p []byte, o Order) uint16 {
	if o == BigEndian {
		return uint16(p[0])<<8 | uint16(p[1])
	}
	return uint16(p[1])<<8 | uint16(p[0])
}

// Decode 判定 p 首部的一个 UTF-16 单元；高代理后的非低代理不被吞掉（Size=2）。
func Decode(p []byte, o Order) Unit {
	if len(p) < 2 {
		return Unit{Kind: UnitIncomplete, Size: len(p)}
	}
	w := read16(p, o)
	switch {
	case 0xD800 <= w && w <= 0xDBFF:
		if len(p) < 4 {
			return Unit{Kind: UnitIncomplete, Size: len(p)}
		}
		w2 := read16(p[2:], o)
		if 0xDC00 <= w2 && w2 <= 0xDFFF {
			r := 0x10000 + (rune(w)-0xD800)<<10 + (rune(w2) - 0xDC00)
			return Unit{Kind: UnitScalar, Size: 4, Rune: r}
		}
		return Unit{Kind: UnitLoneHigh, Size: 2}
	case 0xDC00 <= w && w <= 0xDFFF:
		return Unit{Kind: UnitLoneLow, Size: 2}
	}
	return Unit{Kind: UnitScalar, Size: 2, Rune: rune(w)}
}

// Encode 把标量以字节序 o 写入 b（BMP 2 字节，增补平面 4 字节代理对）。
func Encode(b []byte, r rune, o Order) int {
	if r < 0x10000 {
		put16(b, uint16(r), o)
		return 2
	}
	v := r - 0x10000
	put16(b, 0xD800+uint16(v>>10), o)
	put16(b[2:], 0xDC00+uint16(v&0x3FF), o)
	return 4
}

func put16(b []byte, w uint16, o Order) {
	if o == BigEndian {
		b[0], b[1] = byte(w>>8), byte(w)
	} else {
		b[0], b[1] = byte(w), byte(w>>8)
	}
}

// EncodeLen 返回标量的 UTF-16 编码字节数。
func EncodeLen(r rune) int {
	if !scalar.Valid(r) || r < 0x10000 {
		return 2
	}
	return 4
}
