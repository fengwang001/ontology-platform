// Package dec implements the streaming LZ77 decompressor with per-byte
// validation, corruption classification and output-size protection.
package dec

import (
	"hash/crc32"
	"strconv"

	"ontology/window"
	"ontology/wire"
)

const (
	DefaultOutputLimit = 1 << 30
	minMatch           = 3
	defaultWindowSize  = 32768
)

// Config configures a decompressor. Zero fields take defaults.
type Config struct {
	WindowSize  int
	OutputLimit int64
}

// OffsetError attaches the byte offset inside the compressed stream.
type OffsetError struct {
	Offset int64
	Err    error
}

func (e *OffsetError) Error() string {
	return e.Err.Error() + " at offset " + strconv.FormatInt(e.Offset, 10)
}
func (e *OffsetError) Unwrap() error { return e.Err }

// Decoder is a streaming decompressor. A single Decoder is not safe for
// concurrent use by multiple goroutines.
type Decoder struct {
	win      *window.Window
	limit    int64
	buf      []byte
	consumed int64
	out      []byte
	deliver  int
	crc      uint32
	hdrDone  bool
	done     bool
	terminal error
}

// New validates cfg and creates a decompressor.
func New(cfg Config) (*Decoder, error) {
	if cfg.WindowSize == 0 {
		cfg.WindowSize = defaultWindowSize
	}
	win, err := window.New(cfg.WindowSize)
	if err != nil {
		return nil, err
	}
	if cfg.OutputLimit == 0 {
		cfg.OutputLimit = DefaultOutputLimit
	}
	if cfg.OutputLimit < 0 {
		return nil, wire.ErrBadConfig
	}
	return &Decoder{win: win, limit: cfg.OutputLimit}, nil
}

// Write feeds compressed bytes. After any error the decoder is terminal: later
// calls return the same error. ErrTruncated alone is non-terminal (more bytes
// may complete the pending record).
func (d *Decoder) Write(p []byte) (int, error) {
	if d.terminal != nil {
		return 0, d.terminal
	}
	d.buf = append(d.buf, p...)
	n, err := d.parse()
	d.buf = d.buf[n:]
	d.consumed += int64(n)
	if err != nil && err != wire.ErrTruncated {
		d.terminal = err
	}
	return n, err
}

// Close finalizes: anything short of a completed end record is truncation.
func (d *Decoder) Close() error {
	if d.terminal != nil {
		return d.terminal
	}
	if !d.hdrDone || !d.done || len(d.buf) > 0 {
		d.terminal = d.fail(d.consumed+int64(len(d.buf)), wire.ErrTruncated)
	}
	return d.terminal
}

// Output returns decompressed bytes newly produced since the previous call.
func (d *Decoder) Output() []byte {
	fresh := d.out[d.deliver:]
	d.deliver = len(d.out)
	return fresh
}

func (d *Decoder) fail(off int64, err error) error {
	return &OffsetError{Offset: off, Err: err}
}

func (d *Decoder) parse() (int, error) {
	s := d.buf
	off := int64(0)
	if !d.hdrDone {
		if len(s) < 4 {
			return 0, wire.ErrTruncated
		}
		if string(s[:4]) != string([]byte{wire.Magic0, wire.Magic1, wire.Magic2, wire.Magic3}) {
			return 0, d.fail(0, wire.ErrBadMagic)
		}
		v, n, err := wire.ReadUvarint(s[4:])
		if err == wire.ErrTruncated {
			return 0, wire.ErrTruncated
		}
		if err != nil {
			return 0, d.fail(4, err)
		}
		if v != wire.Version {
			return 0, d.fail(4, wire.ErrBadVersion)
		}
		s, off = s[4+n:], off+int64(4+n)
		d.hdrDone = true
	}
	var rec int64
	for {
		rec = off
		if len(s) == 0 {
			return int(rec), wire.ErrTruncated
		}
		tag, tn, err := wire.ReadTag(s)
		if err != nil {
			return int(rec), d.badRec(rec, err)
		}
		s, off = s[tn:], off+int64(tn)
		switch tag {
		case wire.TagLiteral:
			n, u, err := wire.ReadUvarint(s)
			if err != nil {
				return int(rec), d.badRec(rec, err)
			}
			s, off = s[u:], off+int64(u)
			if n == 0 {
				return int(rec), d.fail(d.consumed+rec, wire.ErrBadRecord)
			}
			if uint64(len(s)) < n {
				return int(rec), wire.ErrTruncated
			}
			if err := d.emitBytes(s[:n]); err != nil {
				return int(rec), d.fail(d.consumed+rec, err)
			}
			s, off = s[n:], off+int64(n)
		case wire.TagMatch:
			dist, u1, e1 := wire.ReadUvarint(s)
			if e1 != nil {
				return int(rec), d.badRec(rec, e1)
			}
			s, off = s[u1:], off+int64(u1)
			length, u2, e2 := wire.ReadUvarint(s)
			if e2 != nil {
				return int(rec), d.badRec(rec, e2)
			}
			s, off = s[u2:], off+int64(u2)
			if err := d.emitMatch(dist, length); err != nil {
				return int(rec), d.fail(d.consumed+rec, err)
			}
		case wire.TagFlush:
		case wire.TagEnd:
			tot, u1, e1 := wire.ReadUvarint(s)
			if e1 != nil {
				return int(rec), d.badRec(rec, e1)
			}
			s, off = s[u1:], off+int64(u1)
			sum, u2, e2 := wire.ReadUvarint(s)
			if e2 != nil {
				return int(rec), d.badRec(rec, e2)
			}
			s, off = s[u2:], off+int64(u2)
			if tot != uint64(len(d.out)) {
				return int(rec), d.fail(d.consumed+rec, wire.ErrLengthMismatch)
			}
			if sum != uint64(d.crc) {
				return int(rec), d.fail(d.consumed+rec, wire.ErrChecksum)
			}
			if len(s) > 0 {
				return int(rec), d.fail(d.consumed+rec, wire.ErrTrailing)
			}
			d.done = true
			return int(off), nil
		default:
			return int(rec), d.fail(d.consumed+rec, wire.ErrBadRecord)
		}
	}
}

func (d *Decoder) badRec(rec int64, err error) error {
	if err == wire.ErrTruncated {
		return wire.ErrTruncated
	}
	return d.fail(d.consumed+rec, err)
}

func (d *Decoder) emitBytes(p []byte) error {
	if int64(len(d.out))+int64(len(p)) > d.limit {
		return wire.ErrOutputLimit
	}
	d.out = append(d.out, p...)
	d.win.Write(p)
	d.crc = crc32.Update(d.crc, crc32.IEEETable, p)
	return nil
}

func (d *Decoder) emitMatch(dist, length uint64) error {
	switch {
	case dist == 0:
		return wire.ErrZeroDistance
	case dist > uint64(d.win.Cap()):
		return wire.ErrDistBeyondWin
	case dist > uint64(d.win.Len()):
		return wire.ErrDistBeyondHist
	case length < minMatch:
		return wire.ErrBadRecord
	}
	if length > uint64(d.limit)-uint64(len(d.out)) {
		return wire.ErrOutputLimit
	}
	start := len(d.out)
	for k := uint64(0); k < length; k++ {
		d.out = append(d.out, d.out[len(d.out)-int(dist)])
	}
	d.win.Write(d.out[start:])
	d.crc = crc32.Update(d.crc, crc32.IEEETable, d.out[start:])
	return nil
}
