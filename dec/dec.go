// Package dec is the streaming LZ77 decompressor with byte-level validation.
package dec

import (
	"bytes"
	"errors"
	"hash"

	"ontology/window"
	"ontology/wire"
)

// Sentinel errors beyond wire.* format errors.
var (
	ErrDistanceZero = errors.New("dec: back-reference distance is zero")
	ErrDistOutput   = errors.New("dec: distance exceeds produced output")
	ErrDistWindow   = errors.New("dec: distance exceeds window capacity")
	ErrLength       = errors.New("dec: declared original length mismatch")
	ErrChecksum     = errors.New("dec: checksum mismatch")
	ErrTrailing     = errors.New("dec: trailing bytes after stream end")
	ErrLimit        = errors.New("dec: output size limit exceeded")
)

// Config configures a decompressor. WindowCap must be > 0.
type Config struct {
	WindowCap int
	MaxOutput uint64
}

// Reader consumes a compressed stream incrementally; not concurrency safe.
// A terminal error is sticky: later Write/Close return the same error.
type Reader struct {
	cfg  Config
	win  *window.Window
	out  bytes.Buffer
	buf  []byte
	pos  int
	abs  int // stream offset of buf start
	hdr  bool
	done bool
	err  error
	hash hash.Hash64
}

// NewReader validates configuration and returns a streaming decompressor.
func NewReader(cfg Config) (*Reader, error) {
	if cfg.WindowCap <= 0 {
		return nil, window.ErrCapacity
	}
	if cfg.MaxOutput == 0 {
		cfg.MaxOutput = 1 << 40
	}
	w, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	return &Reader{cfg: cfg, win: w, hash: wire.NewHash()}, nil
}

// Err returns the sticky terminal error, or nil while still open.
func (r *Reader) Err() error { return r.err }

func (r *Reader) fail(err error) error {
	if r.err == nil {
		r.err = err
	}
	return r.err
}

// Output returns a copy of all decompressed bytes so far (kept after failure).
func (r *Reader) Output() []byte { return append([]byte(nil), r.out.Bytes()...) }

func (r *Reader) emit(p []byte) error {
	if uint64(r.out.Len())+uint64(len(p)) > r.cfg.MaxOutput {
		return ErrLimit
	}
	r.out.Write(p)
	r.hash.Write(p)
	r.win.Write(p)
	return nil
}

// copyMatch appends length bytes from history at distance. Groups of four use
// ordered per-index assignment, correct even when distance < length.
func (r *Reader) copyMatch(distance, length int) error {
	if uint64(r.out.Len())+uint64(length) > r.cfg.MaxOutput {
		return ErrLimit
	}
	b := r.out.Bytes()
	src := b[len(b)-distance:]
	extra := make([]byte, 0, length)
	pick := func(k int) byte {
		if k < distance {
			return src[k]
		}
		return extra[k-distance]
	}
	for k := 0; k < length; {
		jmax := 4
		if distance < 4 || length-k < 4 {
			jmax = 1
		}
		for j := 0; j < jmax && k < length; j++ {
			extra = append(extra, pick(k))
			k++
		}
	}
	return r.emit(extra)
}

// Write feeds one chunk; partial records are retained across calls.
func (r *Reader) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.done {
		return 0, r.fail(r.at(ErrTrailing, r.abs))
	}
	r.buf = append(r.buf, p...)
	if err := r.parse(); err != nil {
		return 0, r.fail(err)
	}
	if r.pos > 0 {
		r.abs += r.pos
		r.buf = append(r.buf[:0], r.buf[r.pos:]...)
		r.pos = 0
	}
	return len(p), nil
}

func (r *Reader) at(err error, off int) error {
	var oe *wire.OffsetError
	if errors.As(err, &oe) {
		err = oe.Err
	}
	return &wire.OffsetError{Err: err, Offset: r.abs + off}
}

func (r *Reader) parse() error {
	for {
		if !r.hdr {
			if len(r.buf)-r.pos < len(wire.Header) {
				return nil
			}
			if err := wire.HeaderValid(r.buf[r.pos:]); err != nil {
				return r.at(err, r.pos+offsetOf(err))
			}
			r.pos += len(wire.Header)
			r.hdr = true
		}
		if r.pos >= len(r.buf) {
			return nil
		}
		tagAt := r.pos
		rd := wire.Reader{B: r.buf, Offset: r.pos}
		tag, _ := rd.Byte()
		switch tag {
		case wire.TagLit:
			n, e := rd.Uvarint()
			if e != nil {
				if errors.Is(e, wire.ErrVarint) {
					return r.at(e, r.pos)
				}
				return nil
			}
			if n > uint64(len(r.buf)) {
				return r.at(wire.ErrVarint, tagAt)
			}
			p, e := rd.Bytes(int(n))
			if e != nil {
				return nil
			}
			if e := r.emit(p); e != nil {
				return r.at(e, tagAt)
			}
			r.pos = rd.Offset
		case wire.TagMatch:
			dv, e := rd.Uvarint()
			if e != nil {
				return hardOrWait(e, r, tagAt)
			}
			lv, e := rd.Uvarint()
			if e != nil {
				return hardOrWait(e, r, tagAt)
			}
			d, l := int(dv), int(lv)
			merr := error(nil)
			switch {
			case d == 0:
				merr = ErrDistanceZero
			case d > r.cfg.WindowCap:
				merr = ErrDistWindow
			case d > r.win.Len():
				merr = ErrDistOutput
			case l == 0 || uint64(l) != lv:
				merr = wire.ErrVarint
			}
			if merr != nil {
				return r.at(merr, tagAt)
			}
			if e := r.copyMatch(d, l); e != nil {
				return r.at(e, tagAt)
			}
			r.pos = rd.Offset
		case wire.TagFlush:
			r.pos = rd.Offset
		case wire.TagEnd:
			ov, e := rd.Uvarint()
			if e != nil {
				return hardOrWait(e, r, tagAt)
			}
			cv, e := rd.Uvarint()
			if e != nil {
				return hardOrWait(e, r, tagAt)
			}
			r.pos = rd.Offset
			if ov != uint64(r.out.Len()) {
				return r.at(ErrLength, tagAt)
			}
			if cv != r.hash.Sum64() {
				return r.at(ErrChecksum, tagAt)
			}
			if r.pos != len(r.buf) {
				return r.at(ErrTrailing, r.pos)
			}
			r.done = true
			r.buf, r.pos = nil, 0
			return nil
		default:
			return r.at(wire.ErrTag, tagAt)
		}
	}
}

func hardOrWait(e error, r *Reader, tagAt int) error {
	if errors.Is(e, wire.ErrVarint) {
		return r.at(e, tagAt)
	}
	return nil // truncated: wait for more bytes
}

func offsetOf(e error) int {
	var oe *wire.OffsetError
	if errors.As(e, &oe) {
		return oe.Offset
	}
	return 0
}

// Close requires a complete stream; a missing/partial trailer is truncation.
func (r *Reader) Close() error {
	if r.err != nil {
		return r.err
	}
	if !r.done {
		return r.fail(&wire.OffsetError{Err: wire.ErrTruncated, Offset: r.abs + len(r.buf)})
	}
	return nil
}
