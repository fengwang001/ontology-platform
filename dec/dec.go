// Package dec 提供流式解压器，逐字节校验压缩流的合法性。
// 单个 Decoder 不是并发安全的。
package dec

import (
	"ontology/window"
	"ontology/wire"
)

// Config 是解压配置；WindowCap 必须为正，MaxOutput 为 0 表示不限。
type Config struct {
	WindowCap int
	MaxOutput uint64
}

// 解析状态。
const (
	stMagic = iota
	stVersion
	stTag
	stLiteral
	stDist
	stEndLen
	stEndSum
	stDone
)

// Decoder 是流式解压器：Write 喂入压缩字节，Close 校验完整性，
// Output 返回已还原的原文。出错后进入终态，后续 Write 返回同一错误。
type Decoder struct {
	win    *window.Window
	maxOut uint64
	out    []byte
	sum    uint64
	state  int
	v      uint64 // 变长整数累加器
	vn     int    // 已读变长整数字节数
	arg    uint64 // 当前记录的长度参数
	endLen uint64
	off    int64 // 已消费的压缩字节数
	recOff int64 // 当前记录起始偏移
	err    error
}

// New 构造解压器；WindowCap <= 0 时拒绝。
func New(cfg Config) (*Decoder, error) {
	w, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	return &Decoder{win: w, maxOut: cfg.MaxOutput, sum: wire.SumSeed}, nil
}

// Output 返回已还原的原文（截断流中它是原文的前缀）。
func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) fail(kind error, off int64) {
	d.err = &wire.Error{Kind: kind, Off: off}
}

// Write 逐字节消费压缩流；返回第一个出错前消费的字节数与错误。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		d.step(b)
		if d.err != nil {
			return i, d.err
		}
	}
	return len(p), nil
}

// Close 校验流已完整结束；否则返回 ErrTruncated（含零长流）。
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.state != stDone {
		d.fail(wire.ErrTruncated, d.off)
	}
	return d.err
}

func (d *Decoder) step(b byte) {
	off := d.off
	d.off++
	switch d.state {
	case stMagic:
		if b != wire.Magic {
			d.fail(wire.ErrMagic, off)
		}
		d.state = stVersion
	case stVersion:
		if b != wire.Version {
			d.fail(wire.ErrVersion, off)
		}
		d.state = stTag
	case stLiteral:
		d.emit(b)
		d.arg--
		if d.arg == 0 {
			d.state = stTag
		}
	case stDone:
		d.fail(wire.ErrTrailing, off)
	default: // stTag / stDist / stEndLen / stEndSum：读变长整数
		d.varint(b, off)
	}
}

// varint 累加一个 LEB128 字节；第 10 字节大于 1 即 64 位溢出。
func (d *Decoder) varint(b byte, off int64) {
	if d.vn == 0 && d.state == stTag {
		d.recOff = off
	}
	if d.vn == 9 && b > 1 {
		d.fail(wire.ErrVarint, off)
		return
	}
	d.v |= uint64(b&0x7f) << (7 * d.vn)
	d.vn++
	if b&0x80 != 0 {
		return
	}
	v := d.v
	d.v, d.vn = 0, 0
	d.gotValue(v)
}

func (d *Decoder) gotValue(v uint64) {
	switch d.state {
	case stTag:
		d.arg = v >> 2
		switch v & 3 {
		case wire.TagLiteral:
			if d.maxOut > 0 && uint64(len(d.out))+d.arg > d.maxOut {
				d.fail(wire.ErrOutputLimit, d.recOff)
			} else if d.arg == 0 {
				d.state = stTag
			} else {
				d.state = stLiteral
			}
		case wire.TagBackref:
			d.state = stDist
		case wire.TagFlush:
			d.state = stTag
		default:
			d.state = stEndLen
		}
	case stDist:
		d.backref(v)
	case stEndLen:
		d.endLen = v
		d.state = stEndSum
	case stEndSum:
		switch {
		case d.endLen != uint64(len(d.out)):
			d.fail(wire.ErrLenMismatch, d.recOff)
		case v != d.sum:
			d.fail(wire.ErrChecksum, d.recOff)
		default:
			d.state = stDone
		}
	}
}

// backref 校验距离并在写出前检查输出上限，然后逐字节向前复制。
func (d *Decoder) backref(dist uint64) {
	switch {
	case dist == 0:
		d.fail(wire.ErrDistZero, d.recOff)
		return
	case dist > uint64(d.win.Cap()):
		d.fail(wire.ErrDistWindow, d.recOff)
		return
	case dist > uint64(d.win.Len()):
		d.fail(wire.ErrDistOutput, d.recOff)
		return
	}
	if d.maxOut > 0 && uint64(len(d.out))+d.arg > d.maxOut {
		d.fail(wire.ErrOutputLimit, d.recOff) // 写出之前拒绝
		return
	}
	for k := uint64(0); k < d.arg; k++ {
		d.emit(d.win.Byte(int(dist))) // 逐字节复制，重叠回指天然正确
	}
	d.state = stTag
}

func (d *Decoder) emit(b byte) {
	d.out = append(d.out, b)
	d.win.Append(b)
	d.sum = wire.SumByte(d.sum, b)
}
