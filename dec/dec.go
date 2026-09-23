// Package dec is a streaming, byte-by-byte validator/decompressor.
// A single Reader is not safe for concurrent use; separate instances are.
package dec

import (
	"errors"
	"hash/crc32"

	"ontology/window"
	"ontology/wire"
)

// MaxOutputUnlimited disables the output size guard.
const MaxOutputUnlimited int64 = -1

// Config configures a decompressor.
type Config struct {
	MaxOutput int64 // -1 unlimited; 0 allows only empty output; default -1
}

// Reader incrementally parses one compressed stream.
type Reader struct {
	cfg     Config
	buf     []byte
	off     int // absolute bytes consumed from the stream
	out     []byte
	win     *window.Window
	crc     uint32
	total   int
	hdr     bool
	ended   bool
	finalErr error
}

// NewReader creates a decompressor.
func NewReader(cfg Config) *Reader {
	if cfg.MaxOutput == 0 {
		cfg.MaxOutput = MaxOutputUnlimited
	}
	win, _ := window.New(1)
	return &Reader{cfg: cfg, win: win}
}

// Write feeds an arbitrary chunk of the compressed stream.
func (r *Reader) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	if err := r.parse(); err != nil {
		if r.finalErr == nil {
			r.finalErr = err
		}
		return 0, r.finalErr
	}
	return len(p), nil
}

// Output drains and returns all bytes decoded since the previous Output call.
func (r *Reader) Output() []byte {
	o := r.out
	r.out = nil
	return o
}

// Close reports truncation when the stream ended without a trailer.
func (r *Reader) Close() error {
	if r.finalErr != nil {
		return r.finalErr
	}
	if !r.ended {
		return wire.At(wire.ErrTruncated, r.off+len(r.buf))
	}
	return nil
}

func (r *Reader) fail(kind error) error {
	err := wire.At(kind, r.off)
	r.finalErr = err
	return err
}

func (r *Reader) readVar() (uint64, bool) {
	start := r.off
	savedOff := r.off
	savedBuf := r.buf
	v, n, err := wire.ReadUvarint(r.buf)
	r.off += n
	r.buf = r.buf[n:]
	if errors.Is(err, wire.ErrVarint) {
		r.finalErr = r.failAt(wire.ErrVarint, start)
		return 0, false
	}
	if errors.Is(err, wire.ErrTruncated) {
		r.off = savedOff
		r.buf = savedBuf
		return 0, false
	}
	return v, true
}

func (r *Reader) failAt(kind error, off int) error {
	e := wire.At(kind, off)
	r.finalErr = e
	return e
}

func (r *Reader) emit(b []byte) error {
	if r.cfg.MaxOutput >= 0 && int64(r.total+len(b)) > r.cfg.MaxOutput {
		return r.fail(wire.ErrOutputLimit)
	}
	r.out = append(r.out, b...)
	r.total += len(b)
	r.crc = crc32.Update(r.crc, crc32.IEEETable, b)
	r.win.Write(b)
	return nil
}

func (r *Reader) parse() error {
	if r.finalErr != nil {
		return r.finalErr
	}
	if !r.hdr {
		if len(r.buf) < 5 {
			return nil
		}
		if string(r.buf[:4]) != wire.Magic || r.buf[4] != wire.Version {
			return r.fail(wire.ErrMagic)
		}
		r.off += 5
		r.buf = r.buf[5:]
		capV, ok := r.readVar()
		if !ok {
			return r.finalErr
		}
		chainV, ok := r.readVar()
		if !ok {
			return r.finalErr
		}
		_ = chainV
		win, err := window.New(int(capV))
		if err != nil {
			return r.failAt(wire.ErrMagic, 0)
		}
		r.win = win
		r.hdr = true
	}
	for len(r.buf) > 0 {
		tag := r.buf[0]
		switch tag {
		case wire.TagLiteral:
			r.off++
			r.buf = r.buf[1:]
			n, ok := r.readVar()
			if !ok {
				return r.finalErr
			}
			if uint64(len(r.buf)) < n {
				return nil
			}
			if err := r.emit(r.buf[:n]); err != nil {
				return err
			}
			r.off += int(n)
			r.buf = r.buf[n:]
		case wire.TagMatch:
			r.off++
			r.buf = r.buf[1:]
			distV, ok := r.readVar()
			if !ok {
				return r.finalErr
			}
			lenV, ok := r.readVar()
			if !ok {
				return r.finalErr
			}
			if distV == 0 {
				return r.fail(wire.ErrZeroDistance)
			}
			if distV > uint64(r.total) {
				return r.fail(wire.ErrDistanceOutput)
			}
			if distV > uint64(r.win.Cap()) {
				return r.fail(wire.ErrDistanceWindow)
			}
			if r.cfg.MaxOutput >= 0 && int64(r.total)+int64(lenV) > r.cfg.MaxOutput {
				return r.fail(wire.ErrOutputLimit)
			}
			if err := r.copyMatch(int(distV), int(lenV)); err != nil {
				return err
			}
		case wire.TagFlush:
			r.off++
			r.buf = r.buf[1:]
		case wire.TagEnd:
			r.off++
			r.buf = r.buf[1:]
			totalV, ok := r.readVar()
			if !ok {
				return r.finalErr
			}
			crcV, ok := r.readVar()
			if !ok {
				return r.finalErr
			}
			if totalV != uint64(r.total) {
				return r.fail(wire.ErrLengthMismatch)
			}
			if crcV != uint64(r.crc) {
				return r.fail(wire.ErrChecksum)
			}
			if len(r.buf) > 0 {
				return r.fail(wire.ErrTrailing)
			}
			r.ended = true
			return nil
		default:
			return r.fail(wire.ErrMagic)
		}
	}
	return nil
}

func (r *Reader) copyMatch(dist, length int) error {
	base := r.total - dist
	chunk := make([]byte, 0, 4096)
	for i := 0; i < length; i++ {
		if r.cfg.MaxOutput >= 0 && int64(r.total) >= r.cfg.MaxOutput {
			return r.fail(wire.ErrOutputLimit)
		}
		var b byte
		pos := base + i
		if pos < r.total {
			b = r.win.At(pos)
		} else if pos >= r.total-len(chunk) {
			b = chunk[pos-(r.total-len(chunk))]
		} else {
			b = r.win.At(pos)
		}
		chunk = append(chunk, b)
		if len(chunk) == cap(chunk) || i == length-1 {
			if err := r.emit(chunk); err != nil {
				return err
			}
			chunk = chunk[:0]
		}
	}
	return nil
}
