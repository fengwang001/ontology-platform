// Package u8 用自写的字节级状态机解码与编码 UTF-8。
// 禁止使用 unicode/utf8、[]rune(string)、for range 字符串等隐式解码。
package u8

import "ontology/scalar"

// Kind 是一次解码事件的种类。
type Kind uint8

const (
	// Scalar 表示一个合法标量，值在 R 中。
	Scalar Kind = iota
	// Illegal 表示一个非法单元，恰好替换成一个 U+FFFD。
	Illegal
)

// Event 是 Feed 吐出的一个解码事件。
type Event struct {
	Kind Kind
	R    rune
	// Size 是本事件从输入流吞掉的字节数。
	Size int
}

// Decoder 是可跨 Write 的增量 UTF-8 解码器。单个实例非并发安全。
type Decoder struct {
	buf    [3]byte // 未定前缀：首字节 + 至多两个后继
	n      int     // buf 中有效字节数
	need   int     // 当前序列期望总长度
	checks int64
}

// Checks 返回进入解码器后被检查过的字节总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回尚未判定、正在缓存等待后续字节的前缀长度（上限 3）。
func (d *Decoder) Pending() int { return d.n }

func leadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2:
		return 0 // 80-BF 游离后继、C0/C1 非法首字节
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF5:
		return 4
	default:
		return 0 // F5-FF
	}
}

// secondOK 按 DESIGN 表判定首字节后的第二字节是否落在合法区间。
func secondOK(first, b byte) bool {
	if b < 0x80 || b > 0xBF {
		return false
	}
	switch first {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Feed 喂入一批字节，对每个可判定事件回调 fn。跨调用的未定前缀被缓存。
func (d *Decoder) Feed(p []byte, fn func(Event)) {
	i := 0
	for i < len(p) {
		d.checks++
		b := p[i]
		if d.n == 0 {
			if b < 0x80 {
				fn(Event{Scalar, rune(b), 1})
				i++
				continue
			}
			d.need = leadLen(b)
			if d.need == 0 {
				fn(Event{Illegal, scalar.Replacement, 1})
				i++
				continue
			}
			d.buf[0] = b
			d.n = 1
			i++
			continue
		}
		// 已有首字节；b 是期望后继。
		ok := cont(b)
		if ok && d.n == 1 {
			ok = secondOK(d.buf[0], b)
		}
		if !ok {
			d.n = 0 // 只吞首字节；b 留给下一拍重新解析
			fn(Event{Illegal, scalar.Replacement, 1})
			continue
		}
		d.buf[d.n] = b
		d.n++
		i++
		if d.n < d.need {
			continue
		}
		fn(Event{Scalar, decodeBuf(d.buf[:d.need]), d.need})
		d.n = 0
	}
}

func decodeBuf(b []byte) rune {
	switch len(b) {
	case 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	default:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 |
			rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
}

// Truncated 在流结束时调用：若残留未定前缀，返回其长度（替换模式输出一个
// U+FFFD，严格模式为截断错误）；无残留返回 0。
func (d *Decoder) Truncated() int {
	if d.n == 0 {
		return 0
	}
	n := d.n
	d.n = 0
	return n
}

// Encode 把一个标量编码成 UTF-8 追加到 dst 后返回。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}

// ResyncStart 供 par 使用：输入 p 是某段起点 i 之前最多 3 字节（按原顺序，
// 最后一个字节紧邻切点）。返回该段真正应开始解析的字节在 p 中的下标；
// 返回 len(p) 表示切点本身就是合法起点。
func ResyncStart(prev []byte) int {
	n := len(prev)
	for j := 1; j <= 3 && j <= n; j++ {
		b := prev[n-j]
		l := leadLen(b)
		if l == 0 || l <= j {
			continue // 非法/非首字节，或序列在切点前已结束（归左段）
		}
		// 首字节越过切点：已见到的后继必须仍是合法前缀，否则该非法
		// 单元已在见到坏字节时归左段，切点不动。
		valid := true
		for k := n - j + 1; k < n; k++ {
			if !cont(prev[k]) {
				valid = false
				break
			}
			if k == n-j+1 && !secondOK(b, prev[k]) {
				valid = false
				break
			}
		}
		if valid {
			return n - j
		}
	}
	return n
}
