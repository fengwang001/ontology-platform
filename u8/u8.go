// Package u8 做 UTF-8 字节序列的逐标量解码与编码，依赖 scalar。
package u8

import "ontology/scalar"

// MaxPrefix 是跨切分点待决前缀的硬上限（字节）。
const MaxPrefix = 3

// Unit 是一次解码结果。Bad 为真表示一个非法单元，Rune 恒为 U+FFFD。
type Unit struct {
	Rune rune
	Bad  bool
	Size int // 该单元在输入中占用（吞掉）的字节数
}

// Decode 解码 prefix（未决前缀，长度 <4）拼接 p 后的下一个单元。
// done 表示流已结束（用于判定截断）。返回单元、剩余字节与剩余前缀；
// incomplete 为真表示是仍需等待后续字节的合法前缀，此时其余返回值无意义。
func Decode(prefix, p []byte, done bool) (u Unit, rest, leftover []byte, incomplete bool) {
	var b0 byte
	plen := len(prefix)
	if plen > 0 {
		b0 = prefix[0]
	} else {
		if len(p) == 0 {
			return incompleteTrunc(done)
		}
		b0 = p[0]
	}
	switch {
	case b0 < 0x80:
		return finish(Unit{Rune: rune(b0), Size: 1}, prefix, p)
	case b0 >= 0xC2 && b0 <= 0xDF:
		return need(b0, 1, 0x80, 0xBF, prefix, p, done)
	case b0 == 0xE0:
		return need(b0, 2, 0xA0, 0xBF, prefix, p, done)
	case b0 >= 0xE1 && b0 <= 0xEC, b0 == 0xEE, b0 == 0xEF:
		return need(b0, 2, 0x80, 0xBF, prefix, p, done)
	case b0 == 0xED:
		return need(b0, 2, 0x80, 0x9F, prefix, p, done)
	case b0 == 0xF0:
		return need(b0, 3, 0x90, 0xBF, prefix, p, done)
	case b0 >= 0xF1 && b0 <= 0xF3:
		return need(b0, 3, 0x80, 0xBF, prefix, p, done)
	case b0 == 0xF4:
		return need(b0, 3, 0x80, 0x8F, prefix, p, done)
	default: // 80..BF 续字节开头，或 C0 C1 F5..FF
		return finish(Unit{Rune: scalar.RuneError, Bad: true, Size: 1}, prefix, p)
	}
}

func incompleteTrunc(done bool) (Unit, []byte, []byte, bool) {
	if done {
		return Unit{}, nil, nil, false
	}
	return Unit{}, nil, nil, true
}

// get 取"逻辑序列"（prefix 后接 p）中第 i（从 0 起）个字节；ok 表示存在。
func get(prefix, p []byte, i int) (byte, bool) {
	if i < len(prefix) {
		return prefix[i], true
	}
	j := i - len(prefix)
	if j < len(p) {
		return p[j], true
	}
	return 0, false
}

func need(b0 byte, want int, lo, hi byte, prefix, p []byte, done bool) (Unit, []byte, []byte, bool) {
	b1, ok := get(prefix, p, 1)
	if !ok {
		return waitOrTrunc(1, want, done, prefix, p)
	}
	if b1 < lo || b1 > hi {
		if b1 >= 0x80 && b1 <= 0xBF {
			return badEnd(2, prefix, p) // 首+1续字节被吞，坏字节 b1 重解析
		}
		return badEnd(1, prefix, p) // 坏字节不是续字节，只吞首字节
	}
	for pos := 2; pos <= want; pos++ {
		bc, ok := get(prefix, p, pos)
		if !ok {
			return waitOrTrunc(pos, want, done, prefix, p)
		}
		if bc < 0x80 || bc > 0xBF {
			return badEnd(pos, prefix, p) // 吞首+已确认(pos-1)字节，bc 重解析
		}
	}
	seq := make([]byte, want+1)
	seq[0] = b0
	for i := 1; i <= want; i++ {
		seq[i], _ = get(prefix, p, i)
	}
	return finish(Unit{Rune: decodeValue(seq), Size: len(seq)}, prefix, p)
}

func waitOrTrunc(have, want int, done bool, prefix, p []byte) (Unit, []byte, []byte, bool) {
	if !done {
		return Unit{}, nil, nil, true
	}
	return badEnd(have, prefix, p) // EOF：只吞已收到的合法前缀
}

func badEnd(consume int, prefix, p []byte) (Unit, []byte, []byte, bool) {
	return finish(Unit{Rune: scalar.RuneError, Bad: true, Size: consume}, prefix, p)
}

// finish 消费逻辑序列前 sz 字节，其余拆分为 rest（在 p 中）与 leftover（未消费前缀）。
func finish(u Unit, prefix, p []byte) (Unit, []byte, []byte, bool) {
	sz := u.Size
	if sz <= len(prefix) {
		return u, p, prefix[sz:], false
	}
	return u, p[sz-len(prefix):], nil, false
}

func decodeValue(s []byte) rune {
	var r rune
	switch len(s) {
	case 2:
		r = rune(s[0]&0x1F)<<6 | rune(s[1]&0x3F)
	case 3:
		r = rune(s[0]&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F)
	case 4:
		r = rune(s[0]&0x07)<<18 | rune(s[1]&0x3F)<<12 | rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F)
	}
	return r
}

// Encode 把标量 r 编码为 UTF-8 字节追加到 dst（非法 r 编码为 U+FFFD）。
func Encode(dst []byte, r rune) []byte {
	if !scalar.IsScalar(r) {
		r = scalar.RuneError
	}
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

// RuneLen 返回 r 的 UTF-8 编码字节数。
func RuneLen(r rune) int {
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
