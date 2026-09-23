package dec

import (
	"errors"
	"hash/fnw"

	"ontology/wire"
	"ontology/window"
)

const DefaultWindow = 1 << 15

type Config struct {
	Window    int
	MaxOutput int64
}

type Decoder struct {
	win      *window.Window
	out      []byte
	sum      uint64
	maxOut   int64
	offset   int64
	tag      int
	state    int
	bits     uint
	varValue uint64
	varStart int64
	length   uint64
	distance uint64
	literal  []byte
	done     bool
	err      error
}

func New(cfg Config) (*Decoder, error) {
	if cfg.Window < 0 {
		return nil, wire.ErrInvalidArgument
	}
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	w, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	return &Decoder{win: w, tag: -1, maxOut: cfg.MaxOutput}, nil
}

func (d *Decoder) Write(data []byte) (int, error) {
	n := len(data)
	for _, b := range data {
		if d.err != nil {
			break
		}
		if d.done {
			d.fail(wire.ErrTrailingBytes)
			break
		}
		d.offset++
		if d.offset <= 5 {
			if b != wire.Header()[d.offset-1] {
				d.fail(wire.ErrBadHeader)
			}
			continue
		}
		switch d.state {
		case 0:
			if b > wire.TagEnd {
				d.fail(wire.ErrBadHeader)
			} else {
				d.tag, d.state = int(b), 1
				d.startVarint()
				if b == wire.TagFlush {
					d.state = 0
				}
			}
		case 1:
			d.varByte(b)
		case 2:
			d.literal = append(d.literal, b)
			if uint64(len(d.literal)) == d.length {
				d.emitLiteral()
			}
		}
	}
	return n, d.err
}

func (d *Decoder) startVarint() {
	d.bits, d.varValue, d.varStart = 0, 0, d.offset-1
}

func (d *Decoder) varByte(b byte) {
	if d.bits == 63 && b&0x7f > 1 {
		d.failAt(wire.ErrVarInt, d.varStart)
		return
	}
	d.varValue |= uint64(b&0x7f) << d.bits
	d.bits += 7
	if b >= 0x80 {
		if d.bits > 70 {
			d.failAt(wire.ErrVarInt, d.varStart)
		}
		return
	}
	d.gotValue()
}

func (d *Decoder) gotValue() {
	switch d.tag {
	case wire.TagLiteral:
		d.length, d.state = d.varValue, 2
		if d.exceeds(d.varValue) {
			d.failAt(wire.ErrLengthMismatch, d.varStart)
			return
		}
		d.literal = make([]byte, 0, d.varValue)
	case wire.TagMatch:
		d.matchValue()
	case wire.TagEnd:
		d.endValue()
	}
}

func (d *Decoder) matchValue() {
	switch {
	case d.length == 0:
		d.length = d.varValue
		if d.varValue == 0 {
			d.failAt(wire.ErrZeroDistance, d.varStart)
			return
		}
		d.startVarint()
	case d.distance == 0:
		d.distance = d.varValue
		if d.exceeds(d.length) {
			d.failAt(wire.ErrLengthMismatch, d.varStart)
			return
		}
		d.emitMatch()
	}
}

func (d *Decoder) endValue() {
	if d.length == 0 {
		d.length = d.varValue
		if d.length != uint64(len(d.out)) {
			d.failAt(wire.ErrLengthMismatch, d.varStart)
		}
		d.startVarint()
	} else {
		if d.varValue != d.checksum() {
			d.failAt(wire.ErrChecksum, d.varStart)
			return
		}
		d.done, d.state = true, 0
	}
}

func (d *Decoder) emitLiteral() {
	d.addOutput(d.literal)
	d.resetRecord()
}

func (d *Decoder) emitMatch() {
	dist := int(d.distance)
	switch {
	case dist > len(d.out):
		d.failAt(wire.ErrDistanceTooFar, d.varStart)
		return
	case dist > d.win.Capacity():
		d.failAt(wire.ErrWindowOverflow, d.varStart)
		return
	}
	for range int(d.length) {
		b, _ := d.win.At(dist)
		d.addOutput([]byte{b})
	}
	d.resetRecord()
}

func (d *Decoder) addOutput(data []byte) {
	d.out = append(d.out, data...)
	d.win.Add(data)
	for _, b := range data {
		d.sum ^= uint64(b)
	}
}

func (d *Decoder) resetRecord() {
	d.tag, d.state, d.length, d.distance, d.literal = -1, 0, 0, 0, nil
}

func (d *Decoder) exceeds(n uint64) bool {
	return d.maxOut >= 0 && uint64(d.maxOut) < uint64(len(d.out))+n
}

func (d *Decoder) checksum() uint64 { return d.sum }

func (d *Decoder) fail(err error) { d.failAt(err, d.offset) }

func (d *Decoder) failAt(err error, offset int64) {
	if d.err == nil {
		d.err = wire.OffsetError{Offset: offset, Err: err}
	}
}

func (d *Decoder) Output() []byte { return append([]byte(nil), d.out...) }

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if !d.done {
		return wire.OffsetError{Offset: d.offset, Err: wire.ErrTruncated}
	}
	return nil
}

var _ = errors.New
