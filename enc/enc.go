// Package enc implements a streaming LZ77 compressor whose output is
// independent of Write boundaries, plus deterministic parallel block
// compression. A Compressor is not safe for concurrent use.
package enc

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const maxMatch = 1 << 20

var ErrBadConfig = errors.New("enc: window, chain, blockSize and workers must be positive")
var ErrClosed = errors.New("enc: compressor closed")

// Config tunes a Compressor: window capacity and max chain length.
type Config struct{ Window, Chain int }

// Compressor accumulates a compressed stream; read it with Output.
type Compressor struct {
	win            *window.Window
	m              *match.Matcher
	out, pend, lit []byte
	proc, ins      int
	sum            wire.Sum64
	dirty, closed  bool
}

func newCompressor(cfg Config) *Compressor {
	win, _ := window.New(cfg.Window)
	c := &Compressor{win: win, sum: wire.NewSum64()}
	c.m, _ = match.New(c, cfg.Window, cfg.Chain)
	return c
}

// New creates a Compressor and emits the stream header.
func New(cfg Config) (*Compressor, error) {
	if cfg.Window <= 0 || cfg.Chain <= 0 { return nil, ErrBadConfig }
	c := newCompressor(cfg)
	c.out = wire.AppendHeader(c.out)
	return c, nil
}

// Output returns the compressed bytes emitted so far.
func (c *Compressor) Output() []byte { return c.out }

// ByteAt serves the matcher: window history followed by pending input.
func (c *Compressor) ByteAt(pos int64) (byte, bool) {
	if i := pos - c.win.Total(); i >= 0 {
		if i < int64(len(c.pend)) { return c.pend[i], true }
		return 0, false
	}
	return c.win.ByteAt(pos)
}

// Write buffers p and emits every decision that is already final.
func (c *Compressor) Write(p []byte) error {
	if c.closed { return ErrClosed }
	c.dirty = c.dirty || len(p) > 0
	c.pend = append(c.pend, p...)
	c.sum.Add(p)
	c.process(false)
	return nil
}

func (c *Compressor) process(final bool) {
	for c.proc < len(c.pend) {
		avail := len(c.pend) - c.proc
		if avail < match.MinMatch() && !final { break }
		for c.ins < c.proc && c.ins+match.MinMatch() <= len(c.pend) {
			c.m.Insert(c.win.Total() + int64(c.ins))
			c.ins++
		}
		dist, l := 0, 0
		if avail >= match.MinMatch() {
			dist, l = c.m.Find(c.win.Total()+int64(c.proc), min(avail, maxMatch))
		}
		if l < match.MinMatch() {
			c.lit = append(c.lit, c.pend[c.proc])
			c.win.Put(c.pend[c.proc])
			c.proc++
			continue
		}
		if !final && c.proc+l == len(c.pend) && l < maxMatch { break } // provisional
		c.flushLit()
		c.out = wire.AppendBackref(c.out, dist, l)
		for ; l > 0; l-- {
			c.win.Put(c.pend[c.proc])
			c.proc++
		}
	}
	if c.proc > 1<<16 && c.proc*2 > len(c.pend) {
		c.pend = append([]byte(nil), c.pend[c.proc:]...)
		c.ins, c.proc = c.ins-c.proc, 0
	}
}

func (c *Compressor) flushLit() { c.out = wire.AppendLiteral(c.out, c.lit); c.lit = c.lit[:0] }

// Flush finalizes pending decisions and appends a flush mark, making all
// input so far decodable. Without new input it emits nothing.
func (c *Compressor) Flush() {
	if c.closed || !c.dirty { return }
	c.process(true)
	c.flushLit()
	c.out = wire.AppendFlush(c.out)
	c.dirty = false
}

// Close finalizes the stream with the end record.
func (c *Compressor) Close() {
	if c.closed { return }
	c.closed = true
	c.process(true)
	c.flushLit()
	c.out = wire.AppendEnd(c.out, uint64(c.win.Total()), uint64(c.sum))
}

// CompressParallel compresses data in blockSize-byte blocks with up to
// workers goroutines; each block may backref the previous block's tail
// (preset dictionary). Output is one valid stream, identical for any
// worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 || cfg.Window <= 0 || cfg.Chain <= 0 { return nil, ErrBadConfig }
	n := (len(data) + blockSize - 1) / blockSize
	recs := make([][]byte, n)
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int(next.Add(1) - 1); i < n; i = int(next.Add(1) - 1) {
				lo := i * blockSize
				recs[i] = compressBlock(data[max(lo-cfg.Window, 0):lo], data[lo:min(lo+blockSize, len(data))], cfg)
			}
		}()
	}
	wg.Wait()
	sum := wire.NewSum64()
	sum.Add(data)
	out := wire.AppendHeader(nil)
	for i, r := range recs {
		out = append(out, r...)
		if i+1 < n { out = wire.AppendFlush(out) }
	}
	return wire.AppendEnd(out, uint64(len(data)), uint64(sum)), nil
}

// compressBlock compresses one block with dict as preset match history.
func compressBlock(dict, block []byte, cfg Config) []byte {
	c := newCompressor(cfg)
	c.win.PutAll(dict)
	for i := 0; i+match.MinMatch() <= len(dict); i++ { c.m.Insert(int64(i)) }
	_ = c.Write(block)
	c.process(true)
	c.flushLit()
	return c.out
}
