package dec

import (
	"hash/crc32"

	"ontology/wire"
)

// parseRecord consumes one body record. Returns bytes consumed and whether a
// full record was available.
func (d *Decoder) parseRecord(p []byte) (int, bool, error) {
	start := d.off
	tag := p[0]
	d.off++
	p = p[1:]
	switch tag {
	case wire.TagFlush:
		d.lastFlush = d.out.Len()
		d.flushCnt++
		return d.off - start, true, nil
	case wire.TagLiteral:
		litStart := d.off
		v, q, ok, err := d.readVarint(p)
		if err != nil || !ok {
			return 0, false, err
		}
		n := int(v)
		if uint64(n) > uint64(d.maxOut-d.out.Len()) {
			return 0, false, d.fail(wire.ErrOutputLimit, litStart)
		}
		if len(q) < n {
			d.off = start
			return 0, false, nil
		}
		d.append(q[:n])
		d.off += n
	case wire.TagMatch:
		consumed, ok, err := d.parseMatch(p)
		if err != nil || !ok {
			return 0, false, err
		}
		return consumed, true, nil
	case wire.TagEnd:
		consumed, ok, err := d.parseEnd(p)
		if err != nil || !ok {
			return 0, false, err
		}
		return consumed, true, nil
	default:
		return 0, false, d.fail(wire.ErrBadTag, start)
	}
	return d.off - start, true, nil
}

func (d *Decoder) parseMatch(p []byte) (int, bool, error) {
	start := d.off - 1
	distV, q, ok, err := d.readVarint(p)
	if err != nil || !ok {
		return 0, false, err
	}
	dist := int(distV)
	if dist == 0 {
		return 0, false, d.fail(wire.ErrDistZero, start+1)
	}
	if dist > d.out.Len() {
		return 0, false, d.fail(wire.ErrDistOutput, start+1)
	}
	if d.winCap > 0 && dist > d.winCap {
		return 0, false, d.fail(wire.ErrDistWindow, start+1)
	}
	lenV, _, ok, err := d.readVarint(q)
	if err != nil || !ok {
		return 0, false, err
	}
	length := int(lenV)
	if uint64(length) > uint64(d.maxOut-d.out.Len()) {
		return 0, false, d.fail(wire.ErrOutputLimit, start)
	}
	d.copyBack(dist, length)
	return d.off - start, true, nil
}

func (d *Decoder) parseEnd(p []byte) (int, bool, error) {
	start := d.off - 1
	totalV, q, ok, err := d.readVarint(p)
	if err != nil || !ok {
		return 0, false, err
	}
	crcV, _, ok, err := d.readVarint(q)
	if err != nil || !ok {
		return 0, false, err
	}
	if int(totalV) != d.out.Len() {
		return 0, false, d.fail(wire.ErrLength, start)
	}
	if uint32(crcV) != d.crc {
		return 0, false, d.fail(wire.ErrChecksum, start)
	}
	d.state = 2
	return d.off - start, true, nil
}

// append writes literal bytes and updates the checksum.
func (d *Decoder) append(p []byte) {
	d.out.Write(p)
	d.crc = crc32.Update(d.crc, crc32.IEEETable, p)
}

// copyBack emits a possibly overlapping back-reference. d.out grows through
// aliased slices, so newly written bytes are immediately re-readable: this is
// the byte-at-a-time forward-copy semantics without a per-byte loop.
func (d *Decoder) copyBack(dist, length int) {
	oldLen := d.out.Len()
	total := oldLen + length
	for d.out.Len() < total {
		cur := d.out.Len()
		buf := d.out.Bytes()
		src := buf[cur-dist:]
		n := total - cur
		if n > len(src) {
			n = len(src)
		}
		d.out.Write(src[:n])
	}
	d.crc = crc32.Update(d.crc, crc32.IEEETable, d.out.Bytes()[oldLen:])
}
