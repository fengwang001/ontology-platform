package enc

import (
	"errors"
	"hash"
	"hash/fnv"
	"io"
	"sync"

	"ontology/match"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 15
	DefaultChain  = 64
)

var ErrInvalidConfig = errors.New("invalid compression configuration")

type Config struct{ Window, Chain int }

type Compressor struct {
	out     io.Writer
	matcher *match.Matcher
	pending []byte
	sum     hash.Hash64
	flushed bool
	closed  bool
	length  uint64
}

func NewWriter(out io.Writer, cfg Config) (*Compressor, error) {
	m, err := newMatcher(cfg)
	if err != nil {
		return nil, err
	}
	if _, err = out.Write(wire.Header()); err != nil {
		return nil, err
	}
	return &Compressor{out: out, matcher: m, sum: fnv.New64a()}, nil
}

func newMatcher(cfg Config) (*match.Matcher, error) {
	if cfg.Window < 0 || cfg.Chain < 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.Chain == 0 {
		cfg.Chain = DefaultChain
	}
	return match.New(cfg.Window, cfg.Chain)
}

func (c *Compressor) Write(data []byte) (int, error) {
	if c.closed || len(data) == 0 {
		return 0, closedError(c.closed)
	}
	c.pending = append(c.pending, data...)
	if err := c.encode(false); err != nil {
		return 0, err
	}
	_, _ = c.sum.Write(data)
	c.length += uint64(len(data))
	c.flushed = false
	return len(data), nil
}

func closedError(closed bool) error {
	if closed {
		return errors.New("compressor is closed")
	}
	return nil
}

func (c *Compressor) Flush() error {
	if c.closed {
		return errors.New("compressor is closed")
	}
	if c.flushed {
		return nil
	}
	if err := c.encode(true); err != nil {
		return err
	}
	if _, err := c.out.Write([]byte{wire.TagFlush}); err != nil {
		return err
	}
	c.flushed = true
	return nil
}

func (c *Compressor) Close() error {
	if c.closed {
		return nil
	}
	if err := c.encode(true); err != nil {
		return err
	}
	rec := wire.AppendUvarint(nil, wire.TagEnd)
	rec = wire.AppendUvarint(rec, c.length)
	rec = wire.AppendUvarint(rec, c.sum.Sum64())
	_, err := c.out.Write(rec)
	c.closed = err == nil
	return err
}

func (c *Compressor) encode(final bool) error {
	data, keep := c.pending, 0
	records, keep, err := encodeData(c.matcher, data, final)
	if err != nil {
		return err
	}
	if _, err = c.out.Write(records); err != nil {
		return err
	}
	if keep == 0 {
		c.pending = c.pending[:0]
	} else {
		c.pending = append(c.pending[:0], data[len(data)-keep:]...)
	}
	return nil
}

func encodeData(m *match.Matcher, data []byte, final bool) ([]byte, int, error) {
	out := make([]byte, 0)
	start, pos := 0, 0
	limit := len(data)
	if !final {
		limit -= 2
	}
	if limit < 0 {
		limit = 0
	}
	for pos < len(data) {
		if !final && pos >= limit {
			m.Add(data[start:limit])
			out = appendLiteral(out, data[start:limit]...)
			return out, len(data) - limit, nil
		}
		distance, length := m.Find(data, pos)
		if length < 3 {
			pos++
			continue
		}
		if !final && pos+length > limit {
			m.Add(data[start:limit])
			out = appendLiteral(out, data[start:limit]...)
			return out, len(data) - limit, nil
		}
		m.Add(data[start : pos+length])
		out = appendLiteral(out, data[start:pos]...)
		out = appendMatch(out, distance, length)
		pos += length
		start = pos
	}
	if !final {
		keep := 2
		if len(data)-start < keep {
			keep = len(data) - start
		}
		m.Add(data[start : len(data)-keep])
		out = appendLiteral(out, data[start:len(data)-keep]...)
		return out, keep, nil
	}
	m.Add(data[start:])
	return appendLiteral(out, data[start:]...), 0, nil
}

func appendLiteral(out []byte, data ...byte) []byte {
	if len(data) == 0 {
		return out
	}
	out = wire.AppendUvarint(out, wire.TagLiteral)
	out = wire.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

func appendMatch(out []byte, distance, length int) []byte {
	out = wire.AppendUvarint(out, wire.TagMatch)
	out = wire.AppendUvarint(out, uint64(distance))
	return wire.AppendUvarint(out, uint64(length))
}

func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrInvalidConfig
	}
	blocks, jobs := (len(data)+blockSize-1)/blockSize, make(chan int)
	results := make([][]byte, blocks)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lo, hi := i*blockSize, min((i+1)*blockSize, len(data))
				from := max(0, lo-cfg.Window)
				m, _ := newMatcher(cfg)
				m.Seed(data[from:lo])
				rec, _, err := encodeData(m, data[lo:hi], true)
				results[i] = rec
				_ = err
			}
		}()
	}
	for i := range blocks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	out := wire.Header()
	for _, rec := range results {
		out = append(out, rec...)
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	out = wire.AppendUvarint(out, wire.TagEnd)
	out = wire.AppendUvarint(out, uint64(len(data)))
	return wire.AppendUvarint(out, h.Sum64()), nil
}
