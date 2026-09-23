// Package enc implements the streaming OLZ compressor and parallel blocks.
package enc

import (
	"errors"
	"hash/crc32"
	"io"
	"sync"

	"ontology/match"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 16
	DefaultChain  = 64
)

var ErrConfig = errors.New("enc: invalid configuration")

// Config configures a compressor.
type Config struct{ Window, ChainLimit int }

// Writer is a streaming OLZ compressor. Not safe for concurrent use.
type Writer struct {
	w                  io.Writer
	m                  *match.Matcher
	winCap             int
	pending, lit       []byte
	base, total, since int
	sum                uint32
	hdr, ended, marked bool
}

// NewWriter compresses into w; zero/negative window or chain is rejected.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	if cfg.Window <= 0 || cfg.ChainLimit <= 0 {
		return nil, ErrConfig
	}
	return &Writer{w: w, m: match.New(cfg.Window, cfg.ChainLimit), winCap: cfg.Window}, nil
}

// Examined reports candidate positions inspected so far.
func (c *Writer) Examined() int64 { return c.m.Examined() }

// Write compresses p. Decisions depend only on bytes already seen, so output is
// independent of how the caller segments Write calls.
func (c *Writer) Write(p []byte) (int, error) {
	if c.ended {
		return 0, errors.New("enc: write after close")
	}
	if !c.hdr {
		if _, err := c.w.Write(wire.Header); err != nil {
			return 0, err
		}
		c.hdr = true
	}
	c.sum = crc32.Update(c.sum, crc32.IEEETable, p)
	c.m.Win().Append(p)
	c.pending = append(c.pending, p...)
	c.total += len(p)
	if err := c.process(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush makes all input so far decodable and emits a flush marker. A second
// Flush with no new input emits nothing.
func (c *Writer) Flush() error {
	if c.ended {
		return errors.New("enc: flush after close")
	}
	if err := c.process(true); err != nil {
		return err
	}
	if c.since == 0 && c.marked {
		return nil
	}
	if _, err := c.w.Write(wire.AppendTag(nil, wire.TagFlush)); err != nil {
		return err
	}
	c.marked, c.since = true, 0
	return nil
}

// Close emits the end record; afterwards Write/Flush/Close fail.
func (c *Writer) Close() error {
	if c.ended {
		return errors.New("enc: double close")
	}
	if err := c.process(true); err != nil {
		return err
	}
	b := wire.AppendTag(nil, wire.TagEnd)
	b = wire.AppendUvarint(b, uint64(c.total))
	b = wire.AppendUvarint(b, uint64(c.sum))
	_, err := c.w.Write(b)
	c.ended = true
	return err
}

// process keeps one undecided trailing byte unless finish forces closure.
func (c *Writer) process(finish bool) error {
	for len(c.pending) > 0 && (finish || len(c.pending) > 1) {
		pos := c.base
		dist, ln := c.m.Find(pos, len(c.pending))
		if ln >= match.MinMatch {
			if err := c.emitLit(); err != nil {
				return err
			}
			for k := 0; k < ln; k++ {
				c.m.Insert(pos + k)
			}
			if _, err := c.w.Write(wire.AppendMatch(nil, dist, ln)); err != nil {
				return err
			}
			c.pending, c.base, c.since = c.pending[ln:], c.base+ln, c.since+ln
			continue
		}
		if pos+match.MinMatch <= c.m.Win().Len() {
			c.m.Insert(pos)
		}
		c.lit = append(c.lit, c.pending[0])
		c.pending, c.base, c.since = c.pending[1:], c.base+1, c.since+1
	}
	return c.emitLit()
}

func (c *Writer) emitLit() error {
	if len(c.lit) == 0 {
		return nil
	}
	if _, err := c.w.Write(wire.AppendLiteral(nil, c.lit)); err != nil {
		return err
	}
	c.lit = c.lit[:0]
	return nil
}

// CompressParallel splits data into blockSize chunks compressed by workers
// goroutines; block i may reference up to cfg.Window trailing bytes of block
// i-1 as a preset dictionary. Output is identical for any workers >= 1.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if cfg.Window <= 0 || cfg.ChainLimit <= 0 || blockSize <= 0 || workers <= 0 {
		return nil, ErrConfig
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	parts := make([][]byte, n)
	var idx int64
	var wg sync.WaitGroup
	for w := 0; w < workers && w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(idx)
				if i >= n {
					return
				}
				idx++
				s, e := i*blockSize, (i+1)*blockSize
				if e > len(data) {
					e = len(data)
				}
				d := s - cfg.Window
				if d < 0 {
					d = 0
				}
				parts[i] = compressBlock(data[s:e], data[d:s], cfg)
			}
		}()
	}
	wg.Wait()
	out := append([]byte{}, wire.Header...)
	for _, p := range parts {
		out = append(out, p...)
	}
	out = wire.AppendTag(out, wire.TagFlush)
	out = wire.AppendUvarint(out, uint64(len(data)))
	out = wire.AppendUvarint(out, uint64(wire.Checksum(data)))
	return append(out, wire.TagEnd), nil
}

func compressBlock(block, dict []byte, cfg Config) []byte {
	m := match.New(cfg.Window, cfg.ChainLimit)
	m.Reset(dict)
	m.Win().Append(block)
	base := len(dict)
	var out, lit []byte
	pos := 0
	for pos < len(block) {
		dist, ln := m.Find(base+pos, len(block)-pos)
		if ln >= match.MinMatch {
			if len(lit) > 0 {
				out = wire.AppendLiteral(out, lit)
				lit = lit[:0]
			}
			for k := 0; k < ln; k++ {
				m.Insert(base + pos + k)
			}
			out, pos = wire.AppendMatch(out, dist, ln), pos+ln
			continue
		}
		m.Insert(base + pos)
		lit, pos = append(lit, block[pos]), pos+1
	}
	if len(lit) > 0 {
		out = wire.AppendLiteral(out, lit)
	}
	return out
}
