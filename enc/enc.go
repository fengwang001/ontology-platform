// Package enc implements the streaming LZ77 compressor and deterministic
// parallel block compression.
package enc

import (
	"hash/crc32"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	DefaultWindowSize = 32768
	DefaultChainLimit = 64
)

// Config configures a compressor. Zero fields take defaults.
type Config struct {
	WindowSize int
	ChainLimit int
}

func (c Config) withDefaults() Config {
	if c.WindowSize == 0 {
		c.WindowSize = DefaultWindowSize
	}
	if c.ChainLimit == 0 {
		c.ChainLimit = DefaultChainLimit
	}
	return c
}

// Encoder is a streaming compressor. A single Encoder is not safe for
// concurrent use by multiple goroutines.
type Encoder struct {
	cfg      Config
	win      *window.Window
	m        *match.Matcher
	out      []byte
	pend     []byte
	pendBase int    // absolute stream position of pend[0]
	lit      []byte // pending literal run (absolute bytes)
	total    int
	crc      uint32
	closed   bool
}

// New validates cfg and creates a streaming encoder.
func New(cfg Config) (*Encoder, error) {
	cfg = cfg.withDefaults()
	win, err := window.New(cfg.WindowSize)
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, cfg.ChainLimit)
	if err != nil {
		return nil, err
	}
	return &Encoder{cfg: cfg, win: win, m: m, out: wire.AppendHeader(nil)}, nil
}

func (e *Encoder) byteAt(abs int) byte {
	if abs >= e.pendBase {
		return e.pend[abs-e.pendBase]
	}
	return e.win.ByteAt(abs)
}

// Write appends input and emits all compression records whose final form is
// already determined without seeing future bytes.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, wire.ErrTrailing
	}
	e.crc = crc32.Update(e.crc, crc32.IEEETable, p)
	e.total += len(p)
	e.pend = append(e.pend, p...)
	e.advance(false)
	return len(p), nil
}

func (e *Encoder) advance(force bool) {
	for {
		pos := e.pendBase
		avail := e.pendBase + len(e.pend)
		if !force && avail-pos < match.MinMatch+1 {
			return
		}
		if avail-pos < match.MinMatch {
			break // final tail shorter than MinMatch: literals
		}
		maxLen := avail - pos
		if !force {
			maxLen-- // keep one extra byte: the match might otherwise extend
		}
		d, l := e.m.Find(pos, encSource{e}, avail, maxLen)
		if d == 0 {
			e.lit = append(e.lit, e.pend[0])
			e.commit(1)
			continue
		}
		if len(e.lit) > 0 {
			e.out = wire.AppendLiteral(e.out, e.lit)
			e.lit = e.lit[:0]
		}
		e.out = wire.AppendMatch(e.out, d, l)
		e.commit(l)
	}
	if force && len(e.pend) > 0 {
		e.lit = append(e.lit, e.pend...)
		e.commit(len(e.pend))
	}
	if len(e.lit) > 0 && (force || e.closed) {
		e.out = wire.AppendLiteral(e.out, e.lit)
		e.lit = e.lit[:0]
	}
}

func (e *Encoder) commit(n int) {
	chunk := e.pend[:n]
	e.win.Write(chunk)
	e.m.IndexNew()
	e.pend = e.pend[n:]
	e.pendBase += n
}

// Flush forces all buffered input into records and emits a flush marker.
// With no new input since the previous flush it emits nothing.
func (e *Encoder) Flush() error {
	if e.closed {
		return wire.ErrTrailing
	}
	if len(e.pend) == 0 && len(e.lit) == 0 {
		return nil
	}
	e.advance(true)
	e.out = wire.AppendFlush(e.out)
	return nil
}

// Close finalizes the stream with the end record and returns all bytes.
func (e *Encoder) Close() ([]byte, error) {
	if e.closed {
		return nil, wire.ErrTrailing
	}
	e.closed = true
	e.advance(true)
	e.out = wire.AppendEnd(e.out, uint64(e.total), uint64(e.crc))
	return e.out, nil
}

// Compress is a convenience one-shot compression.
func Compress(data []byte, cfg Config) ([]byte, error) {
	e, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := e.Write(data); err != nil {
		return nil, err
	}
	return e.Close()
}

type encSource struct{ e *Encoder }

func (s encSource) ByteAt(abs int) byte { return s.e.byteAt(abs) }

// Examined returns the number of matcher candidate positions inspected.
func (e *Encoder) Examined() int64 { return e.m.Examined() }

type blockResult struct {
	recs []byte
	exam int64
}

// CompressParallel splits data into fixed blocks compressed by worker goroutines.
// Each block may reference up to the previous block's window-sized tail as a
// preset dictionary. Output is identical for any worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()
	if blockSize <= 0 || workers <= 0 {
		return nil, wire.ErrBadConfig
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if nb == 0 {
		nb = 1
	}
	results := make([]blockResult, nb)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = compressBlock(data, i, blockSize, cfg)
			}
		}()
	}
	for i := 0; i < nb; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := wire.AppendHeader(nil)
	crc := uint32(0)
	for i := 0; i < nb; i++ {
		out = append(out, results[i].recs...)
	}
	crc = crc32.ChecksumIEEE(data)
	out = wire.AppendEnd(out, uint64(len(data)), uint64(crc))
	return out, nil
}

func compressBlock(data []byte, idx, blockSize int, cfg Config) blockResult {
	win, _ := window.New(cfg.WindowSize)
	lo := idx * blockSize
	hi := lo + blockSize
	if hi > len(data) {
		hi = len(data)
	}
	if lo > 0 {
		dlo := lo - cfg.WindowSize
		if dlo < 0 {
			dlo = 0
		}
		win.Write(data[dlo:lo])
	}
	m, _ := match.New(win, cfg.ChainLimit)
	e := &Encoder{cfg: cfg, win: win, m: m, pendBase: win.Total()}
	e.pend = append(e.pend, data[lo:hi]...)
	e.advance(true)
	return blockResult{recs: e.out, exam: m.Examined()}
}
