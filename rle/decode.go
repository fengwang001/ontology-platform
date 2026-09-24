package rle

import (
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Decoder 是逐字节流式严格解码器：每个输入字节检查恰好一次；
// 游程延迟到下一符号确认或 Close 时提交，以判定相邻同符号。
type Decoder struct {
	out                                  strings.Builder
	maxOut, produced                     int64
	examined, rStart, sStart, rLen, rPos int
	cnt, pendN                           runs.Count
	haveDg, lead0, esc, hasPend          bool
	rb                                   [4]byte
	pendR                                rune
	pendOff                              int
	err                                  error
}

// Write 喂入任意切分的字节；出错后解码器终结。每字节仅检查一次。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for _, b := range p {
		d.examined++
		off := d.examined - 1
		var err error
		switch {
		case d.esc:
			d.esc = false
			if b != '\\' && (b < '0' || b > '9') {
				return 0, d.fail(ErrBadEscape, d.sStart)
			}
			err = d.accept(rune(b))
		case d.rLen > 0:
			d.rb[d.rPos] = b
			d.rPos++
			if b&0xC0 != 0x80 {
				err = d.fail(ErrInvalidUTF8, d.sStart)
			} else if d.rPos == d.rLen {
				r, _ := utf8.DecodeRune(d.rb[:d.rLen])
				d.rLen = 0
				if r == utf8.RuneError {
					err = d.fail(ErrInvalidUTF8, d.sStart)
				} else {
					err = d.accept(r)
				}
			}
		case b == '\\':
			d.esc, d.sStart = true, off
		case b >= '0' && b <= '9':
			if !d.haveDg {
				d.haveDg, d.rStart, d.lead0 = true, off, b == '0'
				d.cnt.SetZero()
			}
			d.cnt.AppendDigit(b)
		case b < 0x80:
			d.sStart = off
			err = d.accept(rune(b))
		default:
			d.sStart = off
			if d.rLen = utf8.RuneLen(rune(b)); d.rLen < 0 {
				err = d.fail(ErrInvalidUTF8, off)
			} else {
				d.rPos, d.rb[0] = 1, b
			}
		}
		if err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// accept 在某游程符号完整时做严格性校验并暂存该游程。
func (d *Decoder) accept(r rune) error {
	n, off := runs.NewCount(1), d.sStart
	if d.haveDg {
		n, off, d.haveDg = d.cnt, d.rStart, false
		switch {
		case d.lead0 && n.IsZero():
			return d.fail(ErrCountZero, off)
		case d.lead0:
			return d.fail(ErrLeadingZero, off)
		case n.CmpUint64(1) == 0:
			return d.fail(ErrCountOne, off)
		}
	}
	if d.hasPend && d.pendR == r {
		return d.fail(ErrAdjacent, off)
	}
	if d.hasPend {
		if err := d.emit(d.pendR, d.pendN, d.pendOff); err != nil {
			return err
		}
	}
	d.hasPend, d.pendR, d.pendN, d.pendOff = true, r, n, off
	return nil
}

// emit 以固定缓冲惰性写出游程，并按已产出字节数检查上限。
func (d *Decoder) emit(r rune, n runs.Count, off int) error {
	w := uint64(utf8.RuneLen(r))
	buf := make([]byte, 0, 4096)
	for n.CmpUint64(0) > 0 {
		k := uint64(cap(buf)) / w
		if n.CmpUint64(k) < 0 {
			k = n.Uint64()
		}
		d.produced += int64(k) * int64(w)
		if d.maxOut > 0 && d.produced > d.maxOut {
			return d.fail(ErrOutputLimit, off)
		}
		buf = buf[:0]
		for range k {
			buf = utf8.AppendRune(buf, r)
		}
		d.out.Write(buf)
		n = n.SubUint64(k)
	}
	return nil
}

// Close 校验流尾并提交最后一个游程。
func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
	case d.esc:
		d.fail(ErrTrailingSlash, d.sStart)
	case d.rLen > 0:
		d.fail(ErrInvalidUTF8, d.sStart)
	case d.haveDg:
		d.fail(ErrTrailingCount, d.rStart)
	case d.hasPend:
		d.err = d.emit(d.pendR, d.pendN, d.pendOff)
		d.hasPend = false
	}
	return d.err
}
