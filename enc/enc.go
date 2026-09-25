// Package enc is the streaming LZ77 compressor. Not concurrency-safe.
package enc

import (
	"errors"
	"io"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	defaultWindowCap  = 1 << 15
	defaultChainLimit = 64
	defaultMaxMatch   = 1 << 18
)

var (
	ErrConfig = errors.New("enc: invalid configuration")
	ErrClosed = errors.New("enc: writer closed")
)

type Options struct {
	WindowCap, ChainLimit, MaxMatch int
}

// Writer compresses bytes into its underlying writer.
type Writer struct {
	w        io.Writer
	win      *window.Window
	m        *match.Matcher
	maxMatch int
	pending  []byte
	lits     []byte
	pos      int
	flushed  int
	total    int64
	checksum uint64
	closed   bool
}

func NewWriter(w io.Writer, opts *Options) (*Writer, error) {
	o := normOptions(opts)
	if o.WindowCap < match.MinMatch || o.ChainLimit <= 0 || o.MaxMatch < match.MinMatch {
		return nil, ErrConfig
	}
	c := &Writer{w: w, win: window.New(o.WindowCap), maxMatch: o.MaxMatch, checksum: wire.InitialChecksum}
	c.m = match.New(c.win, o.ChainLimit)
	if _, err := w.Write(wire.Header); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Writer) flushLits() error {
	if len(c.lits) == 0 {
		return nil
	}
	buf := wire.AppendVarint([]byte{wire.TagLiteral}, uint64(len(c.lits)))
	buf = append(buf, c.lits...)
	c.lits = c.lits[:0]
	_, err := c.w.Write(buf)
	return err
}

func (c *Writer) emitMatch(dist, length int) error {
	if err := c.flushLits(); err != nil {
		return err
	}
	buf := wire.AppendVarint([]byte{wire.TagMatch}, uint64(dist))
	buf = wire.AppendVarint(buf, uint64(length))
	if _, err := c.w.Write(buf); err != nil {
		return err
	}
	c.m.InsertRange(c.pos+1, c.pending[:length])
	c.win.AddAll(c.pending[:length])
	c.pos += length
	c.pending = c.pending[length:]
	return nil
}

func (c *Writer) process(force bool) error {
	for len(c.pending) >= match.MinMatch {
		dist, length := c.m.Find(c.pos+1, c.pending, min(c.maxMatch, len(c.pending)))
		if length < match.MinMatch {
			c.lits = append(c.lits, c.pending[0])
			c.m.Insert(c.pos+1, c.pending[0], c.pending[1], c.pending[2])
			c.win.Add(c.pending[0])
			c.pos, c.pending = c.pos+1, c.pending[1:]
			continue
		}
		if !force && length == len(c.pending) {
			break
		}
		if err := c.emitMatch(dist, length); err != nil {
			return err
		}
	}
	if !force {
		return nil
	}
	c.lits = append(c.lits, c.pending...)
	c.m.InsertRange(c.pos+1, c.pending)
	c.win.AddAll(c.pending)
	c.pos += len(c.pending)
	return c.flushLits()
}

func (c *Writer) Write(p []byte) (int, error) {
	if c.closed {
		return 0, ErrClosed
	}
	c.pending = append(c.pending, p...)
	c.total += int64(len(p))
	c.checksum = wire.Checksum(c.checksum, p)
	return len(p), c.process(false)
}

func (c *Writer) Flush() error {
	if c.closed {
		return ErrClosed
	}
	if c.pos == c.flushed {
		return nil
	}
	if err := c.process(true); err != nil {
		return err
	}
	_, err := c.w.Write([]byte{wire.TagFlush})
	c.flushed = c.pos
	return err
}

func (c *Writer) Close() error {
	if c.closed {
		return ErrClosed
	}
	if err := c.process(true); err != nil {
		return err
	}
	buf := wire.AppendVarint([]byte{wire.TagEnd}, uint64(c.total))
	buf = wire.AppendVarint(buf, c.checksum)
	if _, err := c.w.Write(buf); err != nil {
		return err
	}
	c.closed = true
	return nil
}
