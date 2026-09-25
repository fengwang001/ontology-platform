// Package enc implements a streaming LZ77 compressor and parallel block
// compression. A single Writer is not safe for concurrent use.
package enc

import (
	"errors"
	"hash/crc32"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// Config tunes a Writer. Window and MaxChain must be > 0.
type Config struct {
	Window   int
	MaxChain int
	MaxMatch int
}

// DefaultConfig returns the default compressor configuration.
func DefaultConfig() Config { return Config{Window: 1 << 15, MaxChain: 32, MaxMatch: 1 << 16} }

// ErrClosed rejects writes after Close.
var ErrClosed = errors.New("enc: writer closed")

// Writer compresses a stream. Write only buffers; tokenization happens
// at Flush/Close, so output depends solely on input bytes + flush offsets.
type Writer struct {
	w           io.Writer
	win         *window.Window
	m           *match.Matcher
	pend        []byte
	base, total int64
	crc         uint32
	done        bool
	err         error
}

// NewWriter creates a Writer emitting to w (nil cfg = DefaultConfig).
func NewWriter(w io.Writer, cfg *Config) (*Writer, error) {
	c := DefaultConfig()
	if cfg != nil {
		c = *cfg
	}
	win, err := window.New(c.Window) // rejects Window <= 0
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, c.MaxChain, c.MaxMatch) // rejects MaxChain <= 0
	if err != nil {
		return nil, err
	}
	e := &Writer{w: w, win: win, m: m}
	if _, err := w.Write(wire.Header()); err != nil {
		e.err = err
	}
	return e, nil
}

// Examined reports the matcher's candidate counter (for audits).
func (e *Writer) Examined() int64 { return e.m.Examined() }

// Write buffers p; nothing is emitted until Flush or Close.
func (e *Writer) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if e.done {
		return 0, ErrClosed
	}
	e.pend = append(e.pend, p...)
	e.total += int64(len(p))
	e.crc = crc32.Update(e.crc, crc32.IEEETable, p)
	return len(p), nil
}

// Flush emits all pending input plus a flush mark. With no pending
// input it emits nothing at all.
func (e *Writer) Flush() error {
	if e.err != nil {
		return e.err
	}
	if e.done {
		return ErrClosed
	}
	if len(e.pend) == 0 {
		return nil
	}
	out := wire.AppendFlush(tokenize(e.m, e.pend, e.base, nil))
	if _, err := e.w.Write(out); err != nil {
		e.err = err
		return err
	}
	e.win.AppendBytes(e.pend)
	e.base += int64(len(e.pend))
	e.pend = e.pend[:0]
	return nil
}

// Close flushes any pending input and writes the trailer.
func (e *Writer) Close() error {
	if e.done || e.err != nil {
		return e.err
	}
	e.done = true
	out := tokenize(e.m, e.pend, e.base, nil)
	out = wire.AppendEnd(out, uint64(e.total), uint64(e.crc))
	if _, err := e.w.Write(out); err != nil {
		e.err = err
	}
	return e.err
}

// tokenize greedily encodes seg (absolute base) into wire records.
func tokenize(m *match.Matcher, seg []byte, base int64, dst []byte) []byte {
	m.Bind(seg, base)
	lit, i := 0, 0
	for i < len(seg) {
		if len(seg)-i >= match.MinMatch {
			if dist, length := m.Find(base + int64(i)); length >= match.MinMatch {
				if i > lit {
					dst = wire.AppendLiteral(dst, seg[lit:i])
				}
				dst = wire.AppendMatch(dst, dist, length)
				i += length
				lit = i
				continue
			}
		}
		i++
	}
	if lit < len(seg) {
		dst = wire.AppendLiteral(dst, seg[lit:])
	}
	return dst
}

// CompressParallel compresses data in blocks of blockSize using workers
// goroutines. Each block may back-reference the tail of the previous
// block (preset dictionary). The result is one valid stream, byte
// identical for any workers value.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be > 0")
	}
	cfg := DefaultConfig()
	nblocks := max((len(data)+blockSize-1)/blockSize, 1)
	parts := make([][]byte, nblocks)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range jobs {
				lo, hi := b*blockSize, min((b+1)*blockSize, len(data))
				win, _ := window.New(cfg.Window)
				win.AppendBytes(data[max((b-1)*blockSize, lo-cfg.Window, 0):lo])
				m, _ := match.New(win, cfg.MaxChain, cfg.MaxMatch)
				parts[b] = tokenize(m, data[lo:hi], 0, nil)
			}
		}()
	}
	for b := range nblocks {
		jobs <- b
	}
	close(jobs)
	wg.Wait()
	out := wire.Header()
	for _, p := range parts {
		out = append(out, p...)
	}
	return wire.AppendEnd(out, uint64(len(data)), uint64(crc32.ChecksumIEEE(data))), nil
}
