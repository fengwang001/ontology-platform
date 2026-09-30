// Package dec 提供流式 LZ77 解压器，逐字节校验流的合法性；单个实例不是并发安全的。
package dec

import "ontology/window"
import "ontology/wire"

type Config struct {
	Window    int
	MaxOutput uint64
}

const (
	stTag = iota
	stLitLen
	stRefDist
	stRefLen
	stEndLen
	stEndSum
)

type Decoder struct {
	win                    *window.Window
	maxOut                 uint64
	state                  int
	uv                     wire.Uvarint
	litN, dist, total, sum uint64
	off                    int64
	out                    []byte
	err                    error
	ended                  bool
}

func New(cfg Config) (*Decoder, error) {
	w, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	return &Decoder{win: w, maxOut: cfg.MaxOutput, sum: wire.ChecksumOffset}, nil
}
func (d *Decoder) Output() []byte { return d.out }
func (d *Decoder) Write(p []byte) (int, error) {
	for i, b := range p {
		if d.err != nil {
			return i, d.err
		}
		if d.err = d.step(b); d.err != nil {
			return i, d.err
		}
		d.off++
	}
	return len(p), nil
}
func (d *Decoder) Close() error {
	if d.err == nil && !d.ended {
		d.err = &wire.Error{Offset: d.off, Err: wire.ErrTruncated}
	}
	return d.err
}
func (d *Decoder) fail(err error) error { return &wire.Error{Offset: d.off, Err: err} }
func (d *Decoder) emit(b byte) {
	d.win.Push(b)
	d.out = append(d.out, b)
	d.sum = wire.ChecksumAdd(d.sum, b)
	d.total++
}
func (d *Decoder) step(b byte) error {
	if d.off < 4 { // 流头
		if b == wire.Header()[d.off] {
			return nil
		}
		if d.off == 3 {
			return d.fail(wire.ErrBadVersion)
		}
		return d.fail(wire.ErrBadMagic)
	}
	if d.ended {
		return d.fail(wire.ErrTrailingData)
	}
	if d.litN > 0 { // 字面量体
		d.emit(b)
		d.litN--
		return nil
	}
	if d.state == stTag {
		if b == wire.TagFlush {
			return nil
		}
		if b > wire.TagEnd {
			return d.fail(wire.ErrUnknownTag)
		}
		d.state = [4]int{stLitLen, stRefDist, stTag, stEndLen}[b]
		return nil
	}
	v, done, err := d.uv.Add(b) // uvarint 状态
	if err != nil {
		return d.fail(err)
	}
	if done {
		d.uv.Reset()
		return d.onValue(v)
	}
	return nil
}
func (d *Decoder) onValue(v uint64) error {
	next := stTag
	switch d.state {
	case stLitLen:
		if v > d.maxOut-d.total {
			return d.fail(wire.ErrOutputLimit)
		}
		d.litN = v
	case stRefDist:
		d.dist, next = v, stRefLen
	case stRefLen:
		if err := d.backref(v); err != nil {
			return err
		}
	case stEndLen, stEndSum:
		want, werr := d.total, wire.ErrLengthMismatch
		if d.state == stEndSum {
			want, werr = d.sum, wire.ErrChecksumMismatch
		}
		if v != want {
			return d.fail(werr)
		}
		d.ended = d.state == stEndSum
		next = stEndSum
	}
	d.state = next
	return nil
}

func (d *Decoder) backref(length uint64) error {
	if d.dist == 0 {
		return d.fail(wire.ErrZeroDistance)
	}
	if d.dist > uint64(d.win.Cap()) {
		return d.fail(wire.ErrDistanceTooLarge)
	}
	if d.dist > d.total {
		return d.fail(wire.ErrDistanceTooFar)
	}
	if length > d.maxOut-d.total {
		return d.fail(wire.ErrOutputLimit)
	}
	for ; length > 0; length-- {
		d.emit(d.win.At(int(d.dist)))
	}
	return nil
}
