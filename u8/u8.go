package u8

import "ontology/scalar"

// Unit 是一次解码结果：消费长度、是否非法、码点（非法时为 RuneError）。
type Unit struct {
	Size  int
	Bad   bool
	Value rune
}

const RuneError = rune(0xFFFD)
const MaxPending = 3

func isCont(b byte) bool { return b&0xC0 == 0x80 }

// leadLen 返回首字节声明的序列长度；非法首字节类返回 1。
func leadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2: // 80..BF 孤立续字节，C0 C1 过长
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF5:
		return 4
	default: // F5..FF 越界
		return 1
	}
}

// okSecond 判定首字节对应的第二字节合法区间（E0/ED/F0/F4 收窄）。
func okSecond(b0, b1 byte) bool {
	if !isCont(b1) {
		return false
	}
	switch b0 {
	case 0xE0:
		return b1 >= 0xA0
	case 0xED:
		return b1 <= 0x9F
	case 0xF0:
		return b1 >= 0x90
	case 0xF4:
		return b1 <= 0x8F
	}
	return true
}

// DecodeOne 从 p 头部解码一个单元（非法单元的吞字规则见 DESIGN.md）。
// 不负责 EOF 截断：need>0 表示需要更多字节才能定性。
func DecodeOne(p []byte) (u Unit, need int) {
	if len(p) == 0 {
		return Unit{}, 1
	}
	b0 := p[0]
	n := leadLen(b0)
	if n == 1 {
		if b0 < 0x80 {
			return Unit{Size: 1, Value: rune(b0)}, 0
		}
		return Unit{Size: 1, Bad: true, Value: RuneError}, 0
	}
	if len(p) < 2 {
		return Unit{}, n - len(p)
	}
	if !okSecond(b0, p[1]) {
		// 第二字节失配：只吞首字节，失配字节回退另起。
		return Unit{Size: 1, Bad: true, Value: RuneError}, 0
	}
	for i := 2; i < n; i++ {
		if len(p) <= i {
			return Unit{}, n - len(p)
		}
		if !isCont(p[i]) {
			// 合法前缀 + 第一个非法续字节：吞前缀（含已接受续字节）。
			return Unit{Size: i, Bad: true, Value: RuneError}, 0
		}
	}
	var r rune
	switch n {
	case 2:
		r = rune(b0&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(b0&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	case 4:
		r = rune(b0&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	if !scalar.IsScalar(r) {
		return Unit{Size: n, Bad: true, Value: RuneError}, 0
	}
	return Unit{Size: n, Value: r}, 0
}

// EncodeRune 把标量编码为 UTF-8 追加到 dst。
func EncodeRune(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	}
}

// Boundary 返回从 cut 起本流应真正开始解析的字节偏移。
// 回看仅发生在 cut 左侧最多 MaxPending 字节。
func Boundary(b []byte, cut int) int {
	if cut <= 0 {
		return 0
	}
	if cut >= len(b) {
		return len(b)
	}
	s := cut
	for s > 0 && s > cut-MaxPending && isCont(b[s-1]) {
		s--
	}
	if s > 0 {
		l := b[s-1]
		n := leadLen(l)
		if n > 1 && cut-(s-1) < n {
			u, _ := DecodeOne(b[s-1:])
			if end := s - 1 + u.Size; end > cut {
				return end
			}
		}
	}
	return cut
}

// 依赖 scalar 的占位引用，保证依赖方向。
var _ = scalar.MaxRune
