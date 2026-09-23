// Package enc is the streaming LZ77 compressor and the parallel block
// compressor. A single Compressor is not safe for concurrent use.
package enc

import (
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	// DefaultWindow is the preset/default sliding-window capacity.
	DefaultWindow = 1 << 16
	// DefaultChain caps inspected candidates per position.
	DefaultChain = 32
	// DefaultMaxMatch caps a single back-reference length.
	DefaultMaxMatch = 1 << 20
	fnvPrime        = 1099511628211
)

// Config configures a Compressor; zero fields take defaults.
type Config struct{ Window, MaxChain, MaxMatch int }

// Compressor is a streaming compressor writing to an underlying io.Writer.
type Compressor struct {
	m        *match.Matcher
	out      io.Writer
	p        []byte // uncommitted tail
	lit      []byte // pending literal run
	buf      []byte
	total    uint64
	sum      uint64
	maxMatch int
	closed   bool
	dirty    bool // input since the last flush boundary
}

// NewCompressor writes the stream header immediately.
func NewCompressor(w io.Writer, cfg Config) (*Compressor, error) {
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.MaxChain == 0 {
	cfg.MaxChain = DefaultChain
	}
	if cfg.MaxMatch == 0 {
		cfg.MaxMatch = DefaultMaxMatch
	}
	win, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, cfg.MaxChain, cfg.MaxMatch)
	if err != nil {
		return nil, err
	}
	c := &Compressor{m: m, out: w, maxMatch: cfg.MaxMatch,
		sum: 14695981039346656037}
	if _, err = w.Write(wire.Header); err != nil {
		return nil, err
	}
	return c, nil
}

// Write appends input. Only decisions future input cannot change are emitted.
func (c *Compressor) Write(b []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: write after close")
	}
	c.p = append(c.p, b...)
	c.total += uint64(len(b))
	for _, x := range b {
		c.sum = (c.sum ^ uint64(x)) * fnvPrime
	}
	c.dirty = true
	return len(b), c.decide(false)
}

// Flush commits all input and adds a flush marker; a second flush with no new
// input emits nothing. Matches after flush may still point before it.
func (c *Compressor) Flush() error {
	if c.closed {
		return errors.New("enc: flush after close")
	}
	if err := c.decide(true); err != nil {
		return err
	}
	if !c.dirty {
		return nil
	}
	c.dirty = false
	return c.flush(wire.AppendFlush(nil))
}

// Close commits everything and writes the unique stream tail.
func (c *Compressor) Close() error {
	if c.closed {
		return nil
	}
	if err := c.decide(true); err != nil {
		return err
	}
	c.closed = true
	return c.flush(wire.AppendEnd(nil, c.total, c.sum))
}

func (c *Compressor) flush(rec []byte) error {
	_, err := c.out.Write(rec)
	return err
}

// decide consumes the decidable prefix of p. force commits the whole tail.
func (c *Compressor) decide(force bool) error {
	c.buf = c.buf[:0]
	for len(c.p) > 0 {
	if !force && len(c.p) < 2 {
			break // lone tail byte could begin a distance-1 match later
		}
		avail := len(c.p)
		if !force {
			avail-- // a match ending at the tail could still be extended
		}
		dist, ln := c.m.Find(c.p, avail)
		if ln == 0 || (!force && ln == avail && ln < c.maxMatch) {
			c.lit, c.p = append(c.lit, c.p[0]), c.p[1:]
			c.m.Insert(c.lit[len(c.lit)-1])
			continue
		}
		c.emitLit()
		c.buf = wire.AppendMatch(c.buf, dist, ln)
		for i := 0; i < ln; i++ {
			c.m.Insert(c.p[i])
		}
		c.p = c.p[ln:]
	}
	c.emitLit()
	if len(c.buf) > 0 {
		return c.flush(c.buf)
	}
	return nil
}

func (c *Compressor) emitLit() {
	for len(c.lit) > 0 {
		n := len(c.lit)
		if n > 0x7F {
			n = 0x7F
		}
		c.buf = wire.AppendLiteral(c.buf, c.lit[:n])
		c.lit = c.lit[n:]
	}
}

// CompressParallel splits data into fixed blocks compressed by workers
// goroutines. Each block may reference up to DefaultWindow bytes of the
// previous block's tail (preset dictionary). The stream is identical for any
// workers value and is decodable by the ordinary streaming decompressor.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be positive")
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if nb == 0 {
		nb = 1
	}
	outs := make([][]byte, nb)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for bi := range jobs {
				lo, hi := bi*blockSize, (bi+1)*blockSize
				if hi > len(data) {
					hi = len(data)
				}
				dlo := lo - DefaultWindow
				if dlo < 0 {
					dlo = 0
				}
				outs[bi] = compressBlock(data[dlo:lo], data[lo:hi])
			}
		}()
	}
	for bi := 0; bi < nb; bi++ {
		jobs <- bi
	}
	close(jobs)
	wg.Wait()
	out := append([]byte(nil), wire.Header...)
	for _, b := range outs {
		out = append(out, b...)
	}
	return wire.AppendEnd(out, uint64(len(data)), wire.FNV1a64(data)), nil
}

func compressBlock(dict, block []byte) []byte {
	win, _ := window.New(DefaultWindow)
	m, _ := match.New(win, DefaultChain, DefaultMaxMatch)
	for _, x := range dict {
		m.Insert(x)
	}
	var buf []byte
	for off := 0; off < len(block); {
		if dist, ln := m.Find(block[off:], len(block)-off); ln > 0 {
			buf = wire.AppendMatch(buf, dist, ln)
			for _, x := range block[off : off+ln] {
				m.Insert(x)
			}
			off += ln
			continue
		}
		end := off + 0x7F
		if end > len(block) {
			end = len(block)
		}
		buf = wire.AppendLiteral(buf, block[off:end])
		for _, x := range block[off:end] {
			m.Insert(x)
		}
		off = end
	}
	return buf
}
