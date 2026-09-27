// Package enc is the streaming LZ77 compressor (plus deterministic parallel
// block compression). A single Encoder is not safe for concurrent use.
package enc

import (
	"bytes"
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	defaultWindow = 1 << 15
	defaultChain  = 64
	fnvOffset     = uint64(14695981039346656037)
	fnvPrime      = uint64(1099511628211)
)

// Config tunes window capacity and the hash-chain candidate cap.
type Config struct {
	WindowCap int
	MaxChain  int
}

// DefaultConfig returns the built-in configuration shared with the decoder.
func DefaultConfig() Config { return Config{WindowCap: defaultWindow, MaxChain: defaultChain} }

// Examined reports total hash-chain candidate positions inspected so far.
func (e *Encoder) Examined() int64 { return e.mt.Examined() }

// Encoder buffers each Write and only emits records at Flush/Close boundaries,
// which makes output independent of how Write calls are split.
type Encoder struct {
	cfg     Config
	out     io.Writer
	win     *window.Window
	mt      *match.Matcher
	pending bytes.Buffer
	total   int
	hash    uint64
	started bool
	closed  bool
}

// NewEncoder validates the config and immediately writes the stream header.
func NewEncoder(w io.Writer, cfg Config) (*Encoder, error) {
	win, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	mt, err := match.New(win, cfg.MaxChain)
	if err != nil {
		return nil, err
	}
	e := &Encoder{cfg: cfg, out: w, win: win, mt: mt, hash: fnvOffset}
	if _, err := w.Write(wire.Header(nil)); err != nil {
		return nil, err
	}
	e.started = true
	return e, nil
}

// Write only buffers data; no match is decided at a Write boundary.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("enc: write after close")
	}
	e.pending.Write(p)
	return len(p), nil
}

// compressBlock emits literal/match records for seg over the matcher/window.
func compressBlock(mt *match.Matcher, seg []byte, h uint64) ([]byte, uint64) {
	mt.Reset(seg)
	var dst []byte
	lit := 0
	for pos := 0; pos < len(seg); {
		d, l := mt.Find(pos)
		if l >= match.MinMatch {
			if pos > lit {
				dst = wire.Literal(dst, seg[lit:pos])
			}
			dst = wire.Match(dst, d, l)
			mt.Accept(pos, pos+l)
			pos += l
			lit = pos
			continue
		}
		mt.Accept(pos, pos+1)
		pos++
	}
	if lit < len(seg) {
		dst = wire.Literal(dst, seg[lit:])
	}
	for _, b := range seg {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return dst, h
}

// Flush forces the current match to end and makes all input so far decodable.
func (e *Encoder) Flush() error {
	if e.closed {
		return errors.New("enc: flush after close")
	}
	if e.pending.Len() == 0 {
		return nil // a second Flush with no new input emits nothing
	}
	seg := e.pending.Bytes()
	dst, h := compressBlock(e.mt, seg, e.hash)
	if _, err := e.out.Write(dst); err != nil {
		return err
	}
	if _, err := e.out.Write(wire.Flush(nil)); err != nil {
		return err
	}
	e.total += len(seg)
	e.hash = h
	e.pending.Reset()
	return nil
}

// Close emits the trailer with total length and checksum.
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	seg := e.pending.Bytes()
	dst, h := compressBlock(e.mt, seg, e.hash)
	if _, err := e.out.Write(dst); err != nil {
		return err
	}
	e.total += len(seg)
	e.hash = h
	if _, err := e.out.Write(wire.End(nil, e.total, e.hash)); err != nil {
		return err
	}
	e.closed = true
	return nil
}

// CompressParallel compresses fixed-size blocks with workers goroutines. Each
// block may reference up to WindowCap trailing bytes of the previous block; its
// encoding depends only on those bytes, so workers count never changes output.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errors.New("enc: blockSize must be > 0")
	}
	if workers <= 0 {
		workers = 1
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	parts := make([][]byte, n)
	type job struct{ i int }
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				lo := j.i * blockSize
				hi := lo + blockSize
				if hi > len(data) {
					hi = len(data)
				}
				dlo := lo - defaultWindow
				if dlo < 0 {
					dlo = 0
				}
				win, _ := window.New(defaultWindow)
				win.Prefill(data[dlo:lo])
				mt, _ := match.New(win, defaultChain)
				parts[j.i], _ = compressBlock(mt, data[lo:hi], 0)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- job{i}
	}
	close(jobs)
	wg.Wait()
	out := wire.Header(nil)
	for _, p := range parts {
		out = append(out, p...)
	}
	return wire.End(out, len(data), wire.FNV64(data)), nil
}
