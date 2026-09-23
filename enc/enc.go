// Package enc is a streaming LZ77 compressor implementing the wire format.
// A single Writer is not safe for concurrent use.
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
	DefaultWindowCap = 1 << 16
	DefaultMaxChain  = 32
)

// Config configures a compressor.
type Config struct {
	WindowCap int
	MaxChain  int
}

// Writer compresses bytes. Decisions that may extend into a future Write are
// deferred, so the compressed output is independent of Write segmentation.
type Writer struct {
	w        io.Writer
	cfg      Config
	m        *match.Matcher
	pend     []byte // undecided bytes starting at the first uncommitted offset
	lit      []byte // confirmed literals awaiting emission
	totalIn  int
	crc      uint32
	closed   bool
	flushed  bool // last boundary was a flush with nothing since
}

// NewWriter writes a valid header immediately.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	if cfg.WindowCap == 0 {
		cfg.WindowCap = DefaultWindowCap
	}
	if cfg.MaxChain == 0 {
		cfg.MaxChain = DefaultMaxChain
	}
	m, err := match.New(cfg.WindowCap, cfg.MaxChain)
	if err != nil {
		return nil, err
	}
	z := &Writer{w: w, cfg: cfg, m: m}
	if _, err := w.Write(wire.Header(cfg.WindowCap, cfg.MaxChain)); err != nil {
		return nil, err
	}
	return z, nil
}

func (z *Writer) emitLit() error {
	if len(z.lit) == 0 {
		return nil
	}
	if _, err := z.w.Write(wire.Literal(len(z.lit))); err != nil {
		return err
	}
	_, err := z.w.Write(z.lit)
	z.lit = z.lit[:0]
	return err
}

// advance commits as many pending bytes as are decidable. When force is true
// (Flush/Close/end-of-block) a match reaching the pending end is final.
func (z *Writer) advance(force bool) error {
	for len(z.pend) >= match.MinMatch {
		dist, ln := z.m.Find(z.pend[0], z.pend[1], z.pend[2], z.pend[3:])
		if ln >= match.MinMatch && (force || ln < len(z.pend)) {
			if err := z.emitLit(); err != nil {
				return err
			}
			if _, err := z.w.Write(wire.Match(dist, ln)); err != nil {
				return err
			}
			for i := 0; i < ln; i++ {
				z.m.Add(z.pend[i])
			}
			z.pend = z.pend[ln:]
			continue
		}
		if ln >= match.MinMatch {
			break // match may extend with future bytes
		}
		z.m.Add(z.pend[0])
		z.lit = append(z.lit, z.pend[0])
		z.pend = z.pend[1:]
	}
	if force {
		if err := z.emitLit(); err != nil {
			return err
		}
		if len(z.pend) > 0 {
			if _, err := z.w.Write(wire.Literal(len(z.pend))); err != nil {
				return err
			}
			if _, err := z.w.Write(z.pend); err != nil {
				return err
			}
			z.m.Window().Write(z.pend)
			z.pend = z.pend[:0]
		}
	}
	return nil
}

// Write buffers input and emits only settled records.
func (z *Writer) Write(p []byte) (int, error) {
	if z.closed {
		return 0, errors.New("enc: write after close")
	}
	z.pend = append(z.pend, p...)
	z.totalIn += len(p)
	z.crc = crc32.Update(z.crc, crc32.IEEETable, p)
	z.flushed = false
	if err := z.advance(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush forces every byte written so far to become decodable.
func (z *Writer) Flush() error {
	if z.closed {
		return errors.New("enc: flush after close")
	}
	if z.flushed {
		return nil
	}
	if err := z.advance(true); err != nil {
		return err
	}
	if _, err := z.w.Write([]byte{wire.TagFlush}); err != nil {
		return err
	}
	z.flushed = true
	return nil
}

// Close flushes everything and writes the trailer.
func (z *Writer) Close() error {
	if z.closed {
		return errors.New("enc: double close")
	}
	z.closed = true
	if err := z.advance(true); err != nil {
		return err
	}
	_, err := z.w.Write(wire.End(z.totalIn, z.crc))
	return err
}

// Compress returns the compressed form of data.
func Compress(data []byte, cfg Config) ([]byte, error) {
	var buf []byte
	out := &sliceWriter{&buf}
	z, err := NewWriter(out, cfg)
	if err != nil {
		return nil, err
	}
	if _, err := z.Write(data); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

type sliceWriter struct{ b *[]byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	*s.b = append(*s.b, p...)
	return len(p), nil
}

var errBadBlock = errors.New("enc: blockSize must be positive")

// CompressParallel splits data into fixed blocks compressed concurrently.
// Each block may match the last WindowCap bytes of the previous block. Output
// is identical for any positive worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errBadBlock
	}
	if cfg.WindowCap == 0 {
		cfg.WindowCap = DefaultWindowCap
	}
	if cfg.MaxChain == 0 {
		cfg.MaxChain = DefaultMaxChain
	}
	n := (len(data) + blockSize - 1) / blockSize
	blocks := make([]block, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				blocks[i] = compressBlock(data, i, blockSize, cfg)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := append([]byte(nil), wire.Header(cfg.WindowCap, cfg.MaxChain)...)
	for i := range blocks {
		out = append(out, blocks[i].body...)
	}
	out = append(out, wire.End(len(data), crc32.ChecksumIEEE(data))...)
	return out, nil
}

type block struct{ body []byte }

func compressBlock(data []byte, idx, size int, cfg Config) block {
	start := idx * size
	end := start + size
	if end > len(data) {
		end = len(data)
	}
	dictStart := start - cfg.WindowCap
	if dictStart < 0 {
		dictStart = 0
	}
	var buf []byte
	m, _ := match.New(cfg.WindowCap, cfg.MaxChain)
	if dict := data[dictStart:start]; len(dict) > 0 {
		m.Window().Write(dict)
		m, _ = match.FromWindow(m.Window(), cfg.MaxChain)
	}
	z := &Writer{w: &sliceWriter{&buf}, cfg: cfg, m: m}
	_, _ = z.Write(data[start:end])
	_ = z.advance(true)
	return block{body: buf}
}
