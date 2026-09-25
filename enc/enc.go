// Package enc implements the LZ77 compressor. Not safe for concurrent use.
package enc

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const MaxMatchLen = 1 << 20 // caps one backref; bounds the pending buffer

type Config struct{ Window, MaxChain int }

var ErrConfig = errors.New("enc: window and maxChain must be positive")

// Compressor tokenizes input; the emitted bytes depend only on the input
// and the Flush/Close call offsets. Config fields must be positive.
type Compressor struct {
	win                            *window.Window
	m                              *match.Matcher
	out, pend, lit                 []byte
	frontier, ins, arrived, flushd int
	hash                           wire.Hash
	closed                         bool
}

func newBlock(cfg Config, dict []byte) *Compressor {
	win, _ := window.New(cfg.Window + MaxMatchLen)
	m, _ := match.New(win, cfg.Window, cfg.MaxChain)
	win.AppendBytes(dict)
	return &Compressor{win: win, m: m, frontier: len(dict), hash: wire.NewHash()}
}

func New(cfg Config) (*Compressor, error) {
	if cfg.Window < 1 || cfg.MaxChain < 1 {
		return nil, ErrConfig
	}
	c := newBlock(cfg, nil)
	c.out = wire.AppendHeader(c.out)
	return c, nil
}

func (c *Compressor) Write(p []byte) (int, error) {
	c.hash.AddBytes(p)
	c.arrived += len(p)
	for len(p) > 0 {
		n := min(MaxMatchLen-len(c.pend), len(p))
		c.win.AppendBytes(p[:n])
		c.pend = append(c.pend, p[:n]...)
		c.advance(false)
		p = p[n:]
	}
	return len(p), nil
}

func (c *Compressor) advance(final bool) {
	for len(c.pend) > 0 {
		q := c.frontier
		for c.ins+match.MinMatch <= q {
			c.m.Insert(c.ins)
			c.ins++
		}
		dist, length := c.m.Longest(q, min(len(c.pend), MaxMatchLen))
		if length > 0 && (final || length < len(c.pend) || length == MaxMatchLen) {
			c.flushLit()
			c.out = wire.AppendBackref(c.out, dist, length)
			c.frontier += length
			c.pend = c.pend[length:]
		} else if length == 0 {
			c.lit = append(c.lit, c.pend[0])
			c.frontier++
			c.pend = c.pend[1:]
		} else { // match touches arrival end: wait for more input
			return
		}
	}
}

func (c *Compressor) flushLit() {
	if len(c.lit) > 0 {
		c.out = wire.AppendLiteral(c.out, c.lit)
		c.lit = c.lit[:0]
	}
}

// Flush forces out pending input plus a flush marker; no new input emits nothing.
func (c *Compressor) Flush() error {
	if c.arrived == c.flushd {
		return nil
	}
	c.advance(true)
	c.flushLit()
	c.out = wire.AppendFlush(c.out)
	c.flushd = c.arrived
	return nil
}

func (c *Compressor) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	c.advance(true)
	c.flushLit()
	c.out = wire.AppendTrailer(c.out, uint64(c.arrived), c.hash.Sum64())
	return nil
}

func (c *Compressor) Bytes() []byte { return c.out }

// CompressParallel compresses data in blocks; each block may backref the
// previous block's tail. Output is identical for any workers > 0.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize < 1 || workers < 1 {
		return nil, ErrConfig
	}
	cfg := Config{Window: 1 << 20, MaxChain: 64}
	recs := make([][]byte, (len(data)+blockSize-1)/blockSize)
	var at atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Go(func() {
			for i := int(at.Add(1) - 1); i < len(recs); i = int(at.Add(1) - 1) {
				recs[i] = encodeBlock(data, i, blockSize, cfg)
			}
		})
	}
	wg.Wait()
	out := wire.AppendHeader(nil)
	for _, r := range recs {
		out = append(out, r...)
	}
	h := wire.NewHash()
	h.AddBytes(data)
	return wire.AppendTrailer(out, uint64(len(data)), h.Sum64()), nil
}

func encodeBlock(data []byte, i, blockSize int, cfg Config) []byte {
	start := i * blockSize
	dictFrom := max(start-blockSize, start-cfg.Window, 0)
	c := newBlock(cfg, data[dictFrom:start])
	c.Write(data[start:min(start+blockSize, len(data))])
	c.advance(true)
	c.flushLit()
	return c.out
}
