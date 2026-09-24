package enc

import (
	"errors"
	"hash/crc32"
	"io"
	"runtime"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

var ErrConfig = errors.New("invalid compression configuration")

type Config struct {
	Window      int
	Chain       int
	BlockSize   int
	MaxParallel int
}

type Encoder struct {
	cfg Config
	dst io.Writer
	win *window.Window
	mat *match.Matcher
	buf []byte
	sum uint32
	n   int

	started bool
	closed  bool
	dirty   bool
}

func New(w io.Writer, cfg Config) (*Encoder, error) {
	if cfg.Window <= 0 || cfg.Chain <= 0 {
		return nil, ErrConfig
	}
	if cfg.BlockSize <= 0 {
		cfg.BlockSize = 1 << 16
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = runtime.NumCPU()
	}
	win, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	mat, err := match.New(win, cfg.Chain)
	if err != nil {
		return nil, err
	}
	e := &Encoder{cfg: cfg, dst: w, win: win, mat: mat}
	_, err = w.Write(wire.AppendHeader(nil))
	if err != nil {
		return nil, err
	}
	e.started = true
	return e, nil
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrConfig
	}
	e.buf = append(e.buf, p...)
	e.process(false)
	return len(p), nil
}

func (e *Encoder) Flush() error {
	if e.closed {
		return ErrConfig
	}
	if !e.dirty {
		return nil
	}
	e.process(true)
	_, err := e.dst.Write(wire.AppendFlush(nil))
	e.dirty = false
	return err
}

func (e *Encoder) Candidates() int64 { return e.mat.Candidates() }

func (e *Encoder) Close() error {
	if e.closed {
		return ErrConfig
	}
	e.process(true)
	if e.dirty {
		if _, err := e.dst.Write(wire.AppendFlush(nil)); err != nil {
			return err
		}
		e.dirty = false
	}
	e.closed = true
	_, err := e.dst.Write(wire.AppendEnd(nil, e.n, e.sum))
	return err
}

func (e *Encoder) process(force bool) {
	pos := 0
	for pos < len(e.buf) {
		d, l := e.mat.Find(e.buf[pos:])
		if !force && pos+1 < len(e.buf) {
			if _, next := e.mat.Find(e.buf[pos+1:]); next > l {
				pos++
				continue
			}
		}
		if l < match.MinLength {
			if !force {
				break
			}
			pos++
			if pos == len(e.buf) {
				e.emitLiteral(e.buf)
				e.consume(e.buf)
				e.buf = nil
				return
			}
			continue
			}
			if pos > 0 {
			e.emitLiteral(e.buf[:pos])
			e.consume(e.buf[:pos])
			e.buf = e.buf[pos:]
			pos = 0
		}
		if _, err := e.dst.Write(wire.AppendMatch(nil, d, l)); err == nil {
			e.dirty = true
		}
		e.consume(e.buf[:l])
		e.buf = e.buf[l:]
	}
	if force && pos > 0 {
		e.emitLiteral(e.buf[:pos])
		e.consume(e.buf[:pos])
		e.buf = e.buf[pos:]
	}
}

func (e *Encoder) emitLiteral(p []byte) {
	if len(p) > 0 {
		_, _ = e.dst.Write(wire.AppendLiteral(nil, p))
		e.dirty = true
	}
}

func (e *Encoder) consume(p []byte) {
	e.win.Write(p)
	e.mat.Sync(len(p))
	e.sum = crc32.Update(e.sum, crc32.IEEETable, p)
	e.n += len(p)
}

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrConfig
	}
	if workers > runtime.NumCPU() {
		workers = runtime.NumCPU()
	}
	blocks := (len(data) + blockSize - 1) / blockSize
	if blocks == 0 {
		blocks = 1
	}
	results := make([][]byte, blocks)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for i := range results {
		start := i * blockSize
		end := min(start+blockSize, len(data))
		dStart := max(0, start-DefaultWindow)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := encodeBlock(data[dStart:start], data[start:end])
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			results[i] = b
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	out := wire.AppendHeader(nil)
	for _, b := range results {
		out = append(out, b...)
	}
	sum := crc32.ChecksumIEEE(data)
	return wire.AppendEnd(out, len(data), sum), nil
}

func encodeBlock(dict, block []byte) ([]byte, error) {
	win, _ := window.New(DefaultWindow)
	mat, _ := match.New(win, DefaultChain)
	win.Write(dict)
	mat.Sync(len(dict))
	buf := append([]byte(nil), block...)
	e := &Encoder{cfg: Config{Window: DefaultWindow, Chain: DefaultChain}, win: win, mat: mat, buf: buf}
	var out []byte
	e.dst = chunkWriter{&out}
	e.process(true)
	return out, nil
}

type chunkWriter struct{ p *[]byte }

func (w chunkWriter) Write(p []byte) (int, error) {
	*w.p = append(*w.p, p...)
	return len(p), nil
}

const (
	DefaultWindow = 1 << 15
	DefaultChain  = 32
)
