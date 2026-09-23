// Package dec is the streaming, validating LZ77 decompressor.
// Depends on wire and window. A Reader is not concurrency-safe.
package dec

import (
	"errors"

	"ontology/window"
	"ontology/wire"
)

// Config configures a Reader.
type Config struct {
	WindowCap   int
	OutputLimit uint64 // 0 means unlimited
}

// Reader consumes the stream in arbitrary segmentation; unfinished
// records are held back, so no byte is interpreted twice.
type Reader struct {
	win     *window.Window
	r       *wire.Reader
	out     []byte
	sum     uint64
	limit   uint64
	stage   int // 0 header, 1 records, 2 ended
	fatal   error
	started bool
}

// New creates a decompressor.
func New(cfg Config) (*Reader, error) {
	win, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	return &Reader{win: win, limit: cfg.OutputLimit, sum: 14695981039346656037}, nil
}

// Output returns and removes buffered decompressed bytes.
func (d *Reader) Output() []byte { p := d.out; d.out = nil; return p }

// Fatal returns the terminal error, if any.
func (d *Reader) Fatal() error { return d.fatal }

func (d *Reader) fail(e error) error { d.fatal = e; return e }

func (d *Reader) emit(b byte) {
	d.win.Add(b)
	d.out = append(d.out, b)
	d.sum ^= uint64(b)
	d.sum *= 1099511628211 // incremental FNV-1a 64
}

// reserve rejects before any byte of a run is produced (bomb guard).
func (d *Reader) reserve(n uint64) bool {
	if d.limit > 0 && uint64(d.win.Total())+n > d.limit {
		d.fail(wire.At(wire.ErrOutputLimit, d.r.Offset()))
		return false
	}
	return true
}

// Write feeds any continuation of the stream.
func (d *Reader) Write(p []byte) (int, error) {
	if d.fatal != nil {
		return 0, d.fatal
	}
	d.started = true
	if d.r == nil {
		d.r = wire.NewReader(p)
	} else {
		d.r.Feed(p)
	}
	return len(p), d.process()
}

func (d *Reader) process() error {
	if d.stage == 0 {
		if err := d.r.ReadHeader(); err != nil {
			return d.wait(err)
		}
		d.stage = 1
	}
	for d.stage == 1 {
		off := d.r.Offset()
		tag, err := d.r.ReadTag()
		if err != nil {
			return d.wait(err)
		}
		if tag == wire.TagFlush {
			continue
		}
		if err := d.record(tag); err != nil {
			if errors.Is(err, wire.ErrTruncated) {
				d.r.Seek(off)
				return nil
			}
			return d.fail(err)
		}
	}
	return nil
}

func (d *Reader) record(tag byte) error {
	if tag == wire.TagEnd {
		return d.finish()
	}
	n, err := d.r.ReadUvar()
	if err != nil {
		return err
	}
	switch tag {
	case wire.TagLiteral:
		if !d.reserve(n) {
			return d.fatal
		}
		lit, err := d.r.ReadBytes(int(n))
		if err != nil {
			return err
		}
		for _, b := range lit {
			d.emit(b)
		}
	case wire.TagMatch:
		return d.backref(n)
	}
	return nil
}

func (d *Reader) backref(dist64 uint64) error {
	len64, err := d.r.ReadUvar()
	if err != nil {
		return err
	}
	switch {
	case dist64 == 0:
		return wire.At(wire.ErrZeroDistance, d.r.Offset())
	case int(dist64) > d.win.Cap():
		return wire.At(wire.ErrDistanceBeyondWindow, d.r.Offset())
	case dist64 > uint64(d.win.Total()):
		return wire.At(wire.ErrDistanceBeyondOutput, d.r.Offset())
	case len64 < 3:
		return wire.At(wire.ErrBadMatchLength, d.r.Offset())
	}
	if !d.reserve(len64) {
		return d.fatal
	}
	for i := uint64(0); i < len64; i++ {
		d.emit(d.win.At(int(dist64))) // forward byte copy: overlap-safe
	}
	return nil
}

func (d *Reader) finish() error {
	declLen, err := d.r.ReadUvar()
	if err != nil {
		return err
	}
	declSum, err := d.r.ReadUvar()
	if err != nil {
		return err
}
	if declLen != uint64(d.win.Total()) {
		return wire.At(wire.ErrLengthMismatch, d.r.Offset())
}
	if declSum != d.sum {
		return wire.At(wire.ErrChecksum, d.r.Offset())
}
	if err := d.r.CheckEnd(); err != nil {
		return err
}
	d.stage = 2
	return nil
}

func (d *Reader) wait(e error) error {
	if errors.Is(e, wire.ErrTruncated) {
		return nil
	}
	return d.fail(e)
}

// Close requires a complete, validated stream.
func (d *Reader) Close() error {
	if d.fatal != nil {
		return d.fatal
	}
	if !d.started || d.stage != 2 {
		return wire.At(wire.ErrTruncated, 0)
	}
	return nil
}
