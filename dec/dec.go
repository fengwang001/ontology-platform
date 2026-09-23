// Package dec is a streaming LZ77 decompressor that validates every byte of
// the stream and reports corruption offsets. A single Decoder is not safe for
// concurrent use; separate instances are independent.
package dec

import (
	"bytes"
	"errors"
	"hash/crc32"

	"ontology/window"
	"ontology/wire"
)

const DefaultMaxOutput = 1 << 30

type Decoder struct {
	out       bytes.Buffer
	maxOut    int
	off       int
	state     int // 0 header, 1 body, 2 ended, 3 terminal error
	winCap    int
	crc       uint32
	termErr   error
}

// New creates a decoder; maxOut<=0 means DefaultMaxOutput.
func New(maxOut int) *Decoder {
	if maxOut <= 0 {
		maxOut = DefaultMaxOutput
	}
	return &Decoder{maxOut: maxOut, crc: wire.NewCRC()}
}

func (d *Decoder) fail(op error, off int) error {
	e := &wire.CorruptError{Op: op, Offset: off}
	d.state, d.termErr = 3, e
	return e
}

func (d *Decoder) readVarint(b []byte) (uint64, []byte, bool, error) {
	start := d.off
	v, n, ok, err := wire.ReadUvarint(b)
	d.off += n
	if err != nil {
		return 0, nil, false, d.fail(err, start)
	}
	if !ok {
		d.off = start
		return 0, nil, false, nil
	}
	return v, b[n:], true, nil
}

// Write feeds an arbitrary chunk of the compressed stream.
func (d *Decoder) Write(p []byte) (int, error) {
	n := len(p)
	if d.state == 3 {
		return 0, d.termErr
	}
	if d.state == 2 && len(p) > 0 {
		return 0, d.fail(wire.ErrTrailing, d.off)
	}
	for len(p) > 0 {
		if d.state == 2 {
			return n, d.fail(wire.ErrTrailing, d.off)
		}
		if d.state == 0 {
			need, done, err := d.parseHeader(p)
			if err != nil || !done {
				return n, err
			}
			p = p[need:]
			d.state = 1
		}
		consumed, done, err := d.parseRecord(p)
		if err != nil || !done {
			return n, err
		}
		p = p[consumed:]
	}
	return n, nil
}

func (d *Decoder) parseHeader(p []byte) (int, bool, error) {
	const need = 6
	start := d.off
	if len(p) < need {
		return 0, false, nil
	}
	if p[0] != wire.TagHeader {
		return 0, false, d.fail(wire.ErrBadTag, start)
	}
	if !bytes.Equal(p[1:5], wire.Magic[:]) {
		return 0, false, d.fail(wire.ErrMagic, start+1)
	}
	if p[5] != wire.Version {
		return 0, false, d.fail(wire.ErrVersion, start+5)
	}
	d.off += need
	p = p[need:]
	v, q, ok, err := d.readVarint(p)
	if err != nil || !ok {
		return 0, false, err
	}
	d.winCap = int(v)
	if _, _, err := window.New(d.winCap); err != nil {
		return 0, false, d.fail(wire.ErrConfig, d.off)
	}
	if _, q, ok, err = d.readVarint(q); err != nil || !ok {
		return 0, false, err
	}
	return d.off - start, true, nil
}
