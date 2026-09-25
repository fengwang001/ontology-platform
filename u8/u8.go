// Package u8 在字节级别完成 UTF-8 判定与编码，依赖 scalar。
package u8

import "ontology/scalar"

// Class 返回首字节类别：0 ASCII；1 非法首字节；2..4 期望序列长度。
func Class(b byte) int {
	switch {
	case b < 0x80:
		return 0
	case b < 0xC2: // 0x80..0xBF 续字节，0xC0/0xC1 非最短
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF5:
		return 4
	default: // 0xF5..0xFF 越界
		return 1
	}
}

// IsCont 报告 b 是否为续字节 10xxxxxx。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }

// Second 返回受限首字节第二字节的闭区间；ok=false 表示该首字节不受限或非法。
func Second(lead byte) (lo, hi byte, ok bool) {
	switch lead {
	case 0xE0:
		return 0xA0, 0xBF, true
	case 0xED:
		return 0x80, 0x9F, true
	case 0xF0:
		return 0x90, 0xBF, true
	case 0xF4:
		return 0x80, 0x8F, true
	}
	return 0, 0, false
}

// Encode 把合法标量写入 p（容量至少 4），返回写入字节数；非法返回 0。
func Encode(r rune, p []byte) int {
	if !scalar.Valid(r) {
		return 0
	}
	switch {
	case r < 0x80:
		p[0] = byte(r)
		return 1
	case r < 0x800:
		p[0] = 0xC0 | byte(r>>6)
		p[1] = 0x80 | byte(r)&0x3F
		return 2
	case r < 0x10000:
		p[0] = 0xE0 | byte(r>>12)
		p[1] = 0x80 | byte(r>>6)&0x3F
		p[2] = 0x80 | byte(r)&0x3F
		return 3
	default:
		p[0] = 0xF0 | byte(r>>18)
		p[1] = 0x80 | byte(r>>12)&0x3F
		p[2] = 0x80 | byte(r>>6)&0x3F
		p[3] = 0x80 | byte(r)&0x3F
		return 4
	}
}
