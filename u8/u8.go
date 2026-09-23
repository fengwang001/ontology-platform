// Package u8 手写字节级 UTF-8 增量解码与编码（不用 unicode/utf8）。
package u8

import "ontology/scalar"

const (
	// MaxPending 是合法前缀切分缓存的硬上限。
	MaxPending = 3
	// RuneError 非法/截断单元的标量占位。
	RuneError = scalar.Replacement
)

// Decoder 是不回溯的增量 UTF-8 解码器。每个输入字节最多被检查两次。
// 非并发安全：单次只供一个 goroutine 使用。
type Decoder struct {
	pend    [MaxPending]byte
	npend   int
	need    int   // 首字节期望的尾字节总数
	second  bool  // 缓存前缀是否已通过第二字节校验
	checks  int64 // 字节被检查总次数
	consumed int64 // 已消费输入字节数（含当前缓存）
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回尚未形成单元的缓存字节数（恒 ≤ MaxPending）。
func (d *Decoder) Pending() int { return d.npend }

// Consumed 返回已消费的绝对字节数。
func (d *Decoder) Consumed() int64 { return d.consumed }

// Feed 追加 p 中的字节，每形成一个单元回调 fn(r, n)；n 为该单元吞掉的
// 字节数（非法/截断单元 r==RuneError）。返回 p 中被消费的字节数。
func (d *Decoder) Feed(p []byte, fn func(r rune, n int)) int {
	i := 0
	for i < len(p) {
		b := p[i]
		d.checks++
		if d.npend == 0 {
			i++
			d.consumed++
			if b < 0x80 {
				fn(rune(b), 1)
				continue
			}
			need := scalar.ExpectedLen(b)
			if need == 0 {
				fn(RuneError, 1)
				continue
			}
			d.pend[0] = b
			d.npend, d.need, d.second = 1, need-1, false
			continue
		}
		// 等待尾字节：仅当第二字节合法时才收下，否则释放它重新解析。
		if d.npend == 1 && !scalar.SecondOK(d.pend[0], b) {
			d.npend, d.need = 0, 0
			fn(RuneError, 1) // 首字节非法；b 不前进，下轮重解析
			continue
		}
		i++
		d.consumed++
		d.pend[d.npend] = b
		d.npend++
		d.second = true
		if d.npend == d.need+1 {
			d.flush(fn)
		}
	}
	return i
}

func (d *Decoder) flush(fn func(r rune, n int)) {
	b := d.pend[:d.npend]
	r := decodeAssembled(b)
	n := d.npend
	d.npend, d.need = 0, 0
	fn(r, n)
}

// End 标记流结束：残留前缀作为一个截断非法单元输出，返回是否发生截断。
func (d *Decoder) End(fn func(r rune, n int)) bool {
	if d.npend == 0 {
		return false
	}
	n := d.npend
	d.npend, d.need = 0, 0
	fn(RuneError, n)
	return true
}

// Assemble 解码一个已凑齐、第二字节已合法的完整序列；尾字节非法时返回 RuneError。
func decodeAssembled(b []byte) rune {
	var r rune
	switch len(b) {
	case 2:
		r = rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		r = rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	case 4:
		r = rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
	for _, c := range b[1:] {
		if !scalar.IsContinuation(c) {
			return RuneError
		}
	}
	if !scalar.IsScalar(r) {
		return RuneError
	}
	return r
}

// EncodeLen 返回 r 的 UTF-8 编码长度（标量，否则按替换符）。
func EncodeLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case scalar.IsSurrogate(r) || r > scalar.MaxRune:
		return EncodeLen(RuneError)
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode 把 r 追加编码到 dst，非法标量按 U+FFFD 编码。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case scalar.IsSurrogate(r) || r > scalar.MaxRune:
		return Encode(dst, RuneError)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	}
}
