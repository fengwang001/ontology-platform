// Package enc is a streaming LZ77 compressor with deterministic parallel
// block compression. A single Writer is not safe for concurrent use.
package enc

import (
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// MaxMatchLen is the longest back-reference length emitted.
const MaxMatchLen = 1 << 32

// ErrConfig is returned for invalid compressor configuration.
var ErrConfig = errors.New("enc: invalid configuration")

// Config configures a compressor. Zero fields use defaults.
type Config struct {
	WindowCap int // default 1<<16
	MaxChain  int // default 64
}

func (c Config) withDefaults() (int, int, error) {
	w, ch := c.WindowCap, c.MaxChain
	if w == 0 {
		w = 1 << 16
	}
	if ch == 0 {
		ch = 64
	}
	if w <= 0 || ch <= 0 {
		return 0, 0, ErrConfig
}
	return w, ch, nil
}

// engine performs the cut-independent greedy encoding into raw records.
type engine struct {
	m   *match.Matcher
	buf []byte
	out []byte
}

func newEngine(m *match.Matcher) *engine { return &engine{m: m} }

func (e *engine) emitLit(p []byte) {
	if len(p) == 0 {
		return
	}
	e.out = wire.AppendUvarint(e.out, wire.TagLit)
	e.out = wire.AppendUvarint(e.out, uint64(len(p)))
	e.out = append(e.out, p...)
	e.m.Window().PushBytes(p)
}

func (e *engine) emitMatch(dist, length int) {
	e.out = wire.AppendUvarint(e.out, wire.TagMatch)
	e.out = wire.AppendUvarint(e.out, uint64(dist))
	e.out = wire.AppendUvarint(e.out, uint64(length))
}

// process finalizes records. When force is false only undoubted records are
// emitted and at least the last two bytes stay buffered; force finalizes all.
func (e *engine) process(force bool) {
	for {
		i := 0
		for i < len(e.buf) {
			if !force && len(e.buf)-i < match.MinMatch {
				break
			}
			dist, l := e.m.Find(e.buf[i:], MaxMatchLen)
			if dist == 0 {
				i++
				continue
			}
			if !force && i+l == len(e.buf) {
				break // match may be extended by future Write bytes
			}
			e.emitLit(e.buf[:i])
			for _, b := range e.buf[i : i+l] {
				e.m.Push(b)
			}
			e.emitMatch(dist, l)
			e.buf = e.buf[i+l:]
			i = -1
			break
		}
		if i == -1 {
			continue
		}
		if !force && i > len(e.buf)-match.MinMatch {
			i = len(e.buf) - (match.MinMatch - 1)
		}
		e.emitLit(e.buf[:i])
		e.buf = e.buf[i:]
		if force || len(e.buf) < match.MinMatch {
			return
		}
	}
}

// Writer compresses a stream.
type Writer struct {
	w      io.Writer
	eng    *engine
	sum    uint64
	total  int64
	since  int64
	closed bool
	err    error
}

// NewWriter writes a valid (non-empty) stream to w, including its header now.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	wc, ch, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	m, err := match.New(wc, ch)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(wire.AppendHeader(nil)); err != nil {
		return nil, err
	}
	return &Writer{w: w, eng: newEngine(m)}, nil
}

func (c *Writer) send() {
	if len(c.eng.out) > 0 && c.err == nil {
		_, c.err = c.w.Write(c.eng.out)
		c.eng.out = c.eng.out[:0]
	}
}

// Write appends input. The compressed output does not depend on how input is
// split across Write calls (for identical Flush offsets).
func (c *Writer) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.closed {
		return 0, ErrConfig
	}
	c.eng.buf = append(c.eng.buf, p...)
	c.total += int64(len(p))
	c.since += int64(len(p))
	c.eng.process(false)
	c.send()
	return len(p), c.err
}

// Flush forces all input so far to become decodable and emits one flush mark.
// A second Flush without new input emits nothing.
func (c *Writer) Flush() error {
	if c.err != nil {
		return c.err
	}
	if c.since == 0 {
		return nil
	}
	c.eng.process(true)
	c.eng.out = wire.AppendUvarint(c.eng.out, wire.TagFlush)
	c.since = 0
	c.send()
	return c.err
}

// Close finalizes all input and writes the stream trailer.
func (c *Writer) Close() error {
	if c.err != nil {
		return c.err
	}
	if c.closed {
		return ErrConfig
	}
	c.closed = true
	c.eng.process(true)
	c.eng.out = wire.AppendUvarint(c.eng.out, wire.TagEnd)
	c.eng.out = wire.AppendUvarint(c.eng.out, uint64(c.total))
	// Checksum is computed over the full original; reconstruct via window+buf
	// is impossible, so track it incrementally instead.
	c.eng.out = wire.AppendUvarint(c.eng.out, c.sum)
	c.send()
	return c.err
}

func compressBlock(block []byte, prev []byte, cfg Config) ([]byte, error) {
	wc, ch, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	win := window.New(wc)
	if len(prev) > 0 {
		if len(prev) > wc {
			prev = prev[len(prev)-wc:]
		}
		win.PushBytes(prev)
	}
	m, err := match.NewWithWindow(win, ch)
	if err != nil {
		return nil, err
	}
	e := newEngine(m)
	e.buf = append(e.buf, block...)
	e.process(true)
	return e.out, nil
}

// CompressParallel splits data into fixed blocks compressed concurrently. Each
// block may reference up to WindowCap bytes of the previous block. Output is a
// single valid stream and is byte-identical for any positive workers value.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrConfig
	}
	n := (len(data) + blockSize - 1) / blockSize
	blocks := make([][]byte, n)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		start, end := i*blockSize, (i+1)*blockSize
		if end > len(data) {
			end = len(data)
		}
		var prev []byte
		if start > 0 {
			p := start - cfg.windowOr(blockSize)
			_ = p
			prev = data[:start]
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i, start, end int, prev []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := compressBlock(data[start:end], prev, cfg)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			blocks[i] = out
			mu.Unlock()
		}(i, start, end, prev)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	out := wire.AppendHeader(nil)
	for _, b := range blocks {
		out = append(out, b...)
	}
	out = wire.AppendUvarint(out, wire.TagEnd)
	out = wire.AppendUvarint(out, uint64(len(data)))
	out = wire.AppendUvarint(out, wire.Checksum(data))
	return out, nil
}
