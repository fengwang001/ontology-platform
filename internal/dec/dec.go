package dec

import (
	"errors"
	"hash/crc32"
	"math"

	"ontology/internal/window"
	"ontology/internal/wire"
)

const matchMin = 3

const (
	stHeader = iota
	stTag
	stLitLen
	stLit
	stDist
	stLen
	stEndLen
	stEndCRC
	stDone
)

type Reader struct {
	buf     []byte
	pos     int
	cfg     wire.Config
	win     *window.Ring
	out     []byte
	limit   uint64
	crc     uint32
	state   int
	tag     byte
	varVal  uint64
	varBits uint
	varPos  int
	length  uint64
	dist    uint64
	endLen  uint64
	fatal   error
}

func NewReader(limit uint64) *Reader {
	if limit == 0 {
		limit = math.MaxUint64
	}
	return &Reader{state: stHeader, limit: limit}
}

func (r *Reader) Write(p []byte) (int, error) {
	if r.fatal != nil {
		return 0, r.fatal
	}
	r.buf = append(r.buf, p...)
	if err := r.parse(); err != nil {
		r.fatal = err
	}
	return len(p), r.fatal
}

func (r *Reader) Close() error {
	if r.fatal == nil && (r.state != stDone || r.pos != len(r.buf)) {
		r.fatal = wire.At(r.pos, wire.ErrTruncated)
	}
	return r.fatal
}

func (r *Reader) Output() []byte { return append([]byte(nil), r.out...) }

func (r *Reader) parse() error {
	for {
		switch r.state {
		case stHeader:
			if err := r.parseHeader(); err != nil {
				if errors.Is(err, wire.ErrTruncated) {
					return nil
				}
				return err
			}
		case stTag:
			if r.pos >= len(r.buf) {
				return nil
			}
			r.tag = r.buf[r.pos]
			r.pos++
			switch r.tag {
			case wire.TagLiteral:
				r.beginVar(stLitLen)
			case wire.TagMatch:
				r.beginVar(stDist)
			case wire.TagFlush:
			case wire.TagEnd:
				r.beginVar(stEndLen)
			default:
				return wire.At(r.pos-1, wire.ErrHeader)
			}
		case stLitLen, stDist, stLen, stEndLen, stEndCRC:
			complete, next, value, err := r.readVarint(r.state)
			if err != nil || !complete {
				return err
			}
			switch next {
			case stLit:
				r.length = value
			case stDist:
				r.dist = value
				r.beginVar(stLen)
			case stTag:
				if r.tag == wire.TagMatch {
					if err := r.emitMatch(value); err != nil {
						return err
					}
				}
			case stEndLen:
				r.endLen = value
				r.beginVar(stEndCRC)
			case stEndCRC:
				if r.endLen != uint64(len(r.out)) {
					return wire.At(r.varPos, wire.ErrLengthMismatch)
				}
				if uint32(value) != r.crc {
					return wire.At(r.varPos, wire.ErrChecksum)
				}
				r.state = stDone
				if r.pos != len(r.buf) {
					return wire.At(r.pos, wire.ErrTrailingBytes)
				}
			}
		}
		if r.state == stLit {
			if err := r.emitLiteral(); err != nil {
				return err
			}
		}
		if r.state == stDone || r.pos >= len(r.buf) {
			return nil
		}
	}
}

func (r *Reader) parseHeader() error {
	rd := wire.NewReader(r.buf)
	cfg, err := rd.Header()
	if err != nil {
		return err
	}
	r.cfg, r.win = cfg, window.New(cfg.WindowCap)
	r.pos, r.state = rd.Pos(), stTag
	return nil
}

func (r *Reader) beginVar(next int) {
	r.varVal, r.varBits, r.varPos, r.state = 0, 0, r.pos, next
}

func (r *Reader) readVarint(_ int) (bool, int, uint64, error) {
	for r.varBits < 70 && r.pos < len(r.buf) {
		x := r.buf[r.pos]
		if r.varBits > 63 && x&0x7e != 0 {
			return false, 0, 0, wire.At(r.varPos, wire.ErrInteger)
		}
		r.pos++
		if r.varBits < 64 {
			r.varVal |= uint64(x&0x7f) << r.varBits
		}
		r.varBits += 7
		if x < 0x80 {
			v, next := r.varVal, r.state
			r.varVal, r.varBits = 0, 0
			return true, next, v, nil
		}
	}
	if r.varBits >= 70 {
		return false, 0, 0, wire.At(r.varPos, wire.ErrInteger)
	}
	return false, r.state, 0, nil
}

func (r *Reader) emitLiteral() error {
	n := r.length
	if uint64(len(r.buf)-r.pos) < n {
		return nil
	}
	if n == 0 || uint64(len(r.out))+n > r.limit {
		return wire.At(r.varPos, wire.ErrOutputLimit)
	}
	p := r.buf[r.pos : r.pos+int(n)]
	r.out = append(r.out, p...)
	for _, b := range p {
		r.win.AddByte(b)
	}
	r.crc = crc32.Update(r.crc, crc32.IEEETable, p)
	r.pos += int(n)
	r.state = stTag
	return nil
}

func (r *Reader) emitMatch(length uint64) error {
	distance := r.dist
	if distance == 0 {
		return wire.At(r.varPos, wire.ErrDistanceZero)
	}
	if distance > uint64(r.win.Cap()) {
		return wire.At(r.varPos, wire.ErrDistanceBeyondWindow)
	}
	if distance > uint64(len(r.out)) {
		return wire.At(r.varPos, wire.ErrDistanceBeyondOutput)
	}
	if length < matchMin || uint64(len(r.out))+length > r.limit {
		return wire.At(r.varPos, wire.ErrOutputLimit)
	}
	for i := uint64(0); i < length; i++ {
		b := r.win.At(int(distance))
		r.out = append(r.out, b)
		r.win.AddByte(b)
	}
	r.crc = crc32.Update(r.crc, crc32.IEEETable, r.out[len(r.out)-int(length):])
	r.state = stTag
	return nil
}
