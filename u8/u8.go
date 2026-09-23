package u8

type Status int

const (
	Incomplete Status = iota // 合法前缀但字节不够
	OK
	Invalid
)

// Unit 描述一次逐标量判定的结果。Size 为本单元吞掉的字节数。
type Unit struct {
	Status Status
	R      rune
	Size   int
}

func isCont(b byte) bool { return b&0xC0 == 0x80 }

// IsCont 报告 b 是否为 10xxxxxx 型连续字节。
func IsCont(b byte) bool { return isCont(b) }

// LeadLen 返回首字节对应的期望长度；ok=false 表示该字节永不合法
//（连续字节、C0/C1、F5..FF）。
func LeadLen(b byte) (L int, ok bool) {
	switch {
	case b < 0x80:
		return 1, true
	case b >= 0xC2 && b <= 0xDF:
		return 2, true
	case b >= 0xE0 && b <= 0xEF:
		return 3, true
	case b >= 0xF0 && b <= 0xF4:
		return 4, true
	default:
		return 0, false
	}
}

// SecondOK 判定首字节 lead 与其第二字节是否满足合法区间
//（含第二字节必须是 10xxxxxx）。
func SecondOK(lead, second byte) bool {
	if !isCont(second) {
		return false
	}
	switch {
	case lead == 0xE0:
		return second >= 0xA0
	case lead == 0xED:
		return second <= 0x9F
	case lead == 0xF0:
		return second >= 0x90
	case lead == 0xF4:
		return second <= 0x8F
	default:
		return true
	}
}

// Step 从 p 的开头判定一个单元。p 为空时返回 Incomplete/0。
func Step(p []byte) Unit {
	if len(p) == 0 {
		return Unit{Status: Incomplete}
	}
	b0 := p[0]
	if b0 < 0x80 {
		return Unit{Status: OK, R: rune(b0), Size: 1}
	}
	L, ok := LeadLen(b0)
	if !ok {
		return Unit{Status: Invalid, Size: 1}
	}
	if len(p) < 2 {
		return Unit{Status: Incomplete, Size: len(p)}
	}
	if !SecondOK(b0, p[1]) {
		return Unit{Status: Invalid, Size: 1}
	}
	r := rune(b0&(0xFF>>uint(L))) << 6
	r |= rune(p[1]&0x3F)
	size := 2
	for size < L {
		if size >= len(p) {
			return Unit{Status: Incomplete, Size: size}
		}
		if !isCont(p[size]) {
			return Unit{Status: Invalid, Size: size}
		}
		r = (r << 6) | rune(p[size]&0x3F)
		size++
	}
	return Unit{Status: OK, R: r, Size: size}
}

// EncodeLen 返回标量编码后的 UTF-8 字节数。
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

// AppendEncode 把标量追加到 buf。
func AppendEncode(buf []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(buf, byte(r))
	case r < 0x800:
		return append(buf, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(buf, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(buf, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
