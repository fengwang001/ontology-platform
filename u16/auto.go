package u16

import "ontology/scalar"

// Encode 把标量按指定字节序追加到 dst。
func Encode(dst []byte, r rune, bigEndian bool) []byte {
	if r >= 0x10000 {
		hi, lo := scalar.EncodeSurrogatePair(r)
		return appendUnit(appendUnit(dst, hi, bigEndian), lo, bigEndian)
	}
	return appendUnit(dst, uint16(r), bigEndian)
}

func appendUnit(dst []byte, u uint16, big bool) []byte {
	if big {
		return append(dst, byte(u>>8), byte(u))
	}
	return append(dst, byte(u), byte(u>>8))
}

// Len 返回标量的 UTF-16 字节长度。
func Len(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// AutoDecoder 在流开头按 BOM 自动判定 UTF-16 字节序；无 BOM 时按 LE。
type AutoDecoder struct {
	dec    *Decoder
	pre    [2]byte
	n      int
	hasBOM bool
	bomHit bool
}

// NewAutoDecoder 构造自动字节序解码器（初始字节序未定）。
func NewAutoDecoder() *AutoDecoder { return &AutoDecoder{} }

// HasBOM 报告开头是否检测到 BOM。
func (a *AutoDecoder) HasBOM() bool { return a.hasBOM }

// BigEndian 报告判定结果（未判定时为 false，即默认 LE）。
func (a *AutoDecoder) BigEndian() bool { return a.dec != nil && a.dec.big }

// TakeBOM 一次性返回并清除"刚识别出开头 BOM"标志。
func (a *AutoDecoder) TakeBOM() bool {
	v := a.bomHit
	a.bomHit = false
	return v
}

// Pending 返回挂起字节数（判定阶段或底层解码器）。
func (a *AutoDecoder) Pending() int {
	if a.dec == nil {
		return a.n
	}
	return a.dec.Pending()
}

// Feed 喂入一个字节。
func (a *AutoDecoder) Feed(b byte) (Event, bool) {
	if a.dec != nil {
		return a.dec.Feed(b)
	}
	a.pre[a.n] = b
	a.n++
	if a.n < 2 {
		return Event{}, false
	}
	a.n = 0
	switch {
	case a.pre[0] == 0xFE && a.pre[1] == 0xFF:
		a.hasBOM, a.bomHit = true, true
		a.dec = NewDecoder(true)
	case a.pre[0] == 0xFF && a.pre[1] == 0xFE:
		a.hasBOM, a.bomHit = true, true
		a.dec = NewDecoder(false)
	default:
		a.dec = NewDecoder(false)
		if e, ok := a.dec.Feed(a.pre[0]); ok {
			a.dec.queued, a.dec.q = true, e
		}
		return a.dec.Feed(a.pre[1])
	}
	return Event{}, false // BOM 已在字节序判定时消费
}

// Finish 报告残留：判定阶段不足 2 字节算截断（kind 1）。
func (a *AutoDecoder) Finish() (Event, int) {
	if a.dec == nil {
		if a.n == 0 {
			return Event{}, 0
		}
		return Event{Kind: Bad, R: scalar.RuneError, Len: a.n, Start: 0}, 1
	}
	return a.dec.Finish()
}
