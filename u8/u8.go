
// Package u8 在字节级实现 UTF-8 的逐标量解码与编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// Event 是一次解析结果：r 为标量（非法时为 RuneError），n 为该单元吞掉的字节数。
type Event struct {
	R  rune
	N  int
	OK bool
}

// Decoder 是有状态 UTF-8 解码器；单个实例非并发安全。
type Decoder struct {
	buf    [3]byte
	n      int
	checks int64
}

// Checks 返回本解码器字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回切分缓存中尚未解析的字节数（硬上限 3）。
func (d *Decoder) Pending() int { return d.n }

// Reset 清空解码器状态与计数。
func (d *Decoder) Reset() { d.n, d.checks = 0, 0 }

// LeadLen 返回引导字节 b 期望的序列长度；ASCII 为 1，其余非引导字节为 0。
func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
	return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 0
	}
}

func secondOK(b0, b1 byte) bool {
	switch {
	case b0 == 0xE0:
		return b1 >= 0xA0 && b1 <= 0xBF
	case b0 == 0xED:
		return b1 >= 0x80 && b1 <= 0x9F
	case b0 == 0xF0:
		return b1 >= 0x90 && b1 <= 0xBF
	case b0 == 0xF4:
		return b1 >= 0x80 && b1 <= 0x8F
	default:
		return b1 >= 0x80 && b1 <= 0xBF
	}
}

func decode(b []byte) rune {
	switch len(b) {
	case 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	default:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
}

// Feed 喂入字节并在每解析出一个单元时调用 emit；返回 p 中被消费的字节数，其余留作待决前缀。
func (d *Decoder) Feed(p []byte, emit func(Event)) int {
	i := 0
	for i < len(p) {
		if d.n == 0 && p[i] < 0x80 {
			d.checks++
			emit(Event{R: rune(p[i]), N: 1, OK: true})
			i++
			continue
		}
		if d.n == 0 && LeadLen(p[i]) == 0 {
			d.checks++
			emit(Event{R: scalar.RuneError, N: 1, OK: false})
			i++
			continue
		}
		d.buf[d.n] = p[i]
		d.n++
		d.checks++
		i++
		d.resolve(emit)
	}
	return i
}

func (d *Decoder) resolve(emit func(Event)) {
	for d.n > 0 {
		b0 := d.buf[0]
		need := LeadLen(b0)
		switch {
		case need == 0: // 残留的延续字节
			emit(Event{R: scalar.RuneError, N: 1, OK: false})
			d.shift(1)
		case d.n < need && d.lastContinuationOK():
			if d.n == 2 && !secondOK(b0, d.buf[1]) {
				emit(Event{R: scalar.RuneError, N: 1, OK: false})
				d.shift(1)
				continue
			}
			return // 等待后续字节
		case d.n < need:
			// 第二字节区间非法吞 1；后续延续字节非法吞到它之前的 n-1 个字节。
			if d.n == 2 {
				emit(Event{R: scalar.RuneError, N: 1, OK: false})
				d.shift(1)
			} else {
				emit(Event{R: scalar.RuneError, N: d.n - 1, OK: false})
				d.shift(d.n - 1)
			}
		case !d.lastContinuationOK():
			emit(Event{R: scalar.RuneError, N: d.n - 1, OK: false})
			d.shift(d.n - 1)
		default: // 序列到齐
			if r := decode(d.buf[:d.n]); !scalar.IsValid(r) {
				emit(Event{R: scalar.RuneError, N: d.n, OK: false})
				d.shift(d.n)
			} else {
				emit(Event{R: r, N: d.n, OK: true})
				d.shift(d.n)
			}
		}
	}
}

// lastContinuationOK 报告最新进入缓存的字节（第 2 位起）是否满足结构/区间约束。
func (d *Decoder) lastContinuationOK() bool {
	switch d.n {
	case 2:
		return secondOK(d.buf[0], d.buf[1])
	case 3, 4:
		return d.buf[d.n-1] >= 0x80 && d.buf[d.n-1] <= 0xBF
	default:
		return true
	}
}

func (d *Decoder) shift(k int) {
	for j := k; j < d.n; j++ {
		d.buf[j-k] = d.buf[j]
	}
	d.n -= k
}

// Finish 在流结束时调用：残留的未完成合法前缀作为一个非法单元（长度为残留字节数）输出。
func (d *Decoder) Finish(emit func(Event)) {
	if d.n > 0 {
		emit(Event{R: scalar.RuneError, N: d.n, OK: false})
		d.n = 0
	}
}

// EncLen 返回标量 r 的 UTF-8 编码长度。
func EncLen(r rune) int {
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

// Append 把标量 r 编码为 UTF-8 追加到 b。
func Append(b []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(b, byte(r))
	case r < 0x800:
		return append(b, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(b, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(b, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	}
}
