// Package enc 提供流式 LZ77 压缩器与按块并行压缩；单个 Encoder 实例不是并发安全的。
package enc

import (
	"errors"
	"io"
	"slices"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// Config 是压缩配置；Window 与 MaxChain 必须为正。
type Config struct {
	Window   int
	MaxChain int
}

var ErrInvalidBlock = errors.New("enc: block size and workers must be positive")
var ErrClosed = errors.New("enc: write after close")

type core struct {
	m        *match.Matcher
	buf      []byte
	pos, lit int
	out      []byte
}

func newCore(cfg Config, buf []byte, start int) (*core, error) {
	w, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	m, err := match.New(w, cfg.MaxChain)
	if err != nil {
		return nil, err
	}
	return &core{m: m, buf: buf, pos: start, lit: start}, nil
}

func (c *core) process(final bool) {
	for len(c.buf)-c.pos >= match.MinMatch {
		dist, l := c.m.Longest(c.buf, c.pos)
		if l == 0 {
			c.pos++
			continue
		}
		if c.pos+l == len(c.buf) && !final && l < match.MaxLen {
			return // 匹配延伸到缓冲末尾，等待更多输入
		}
		if c.lit < c.pos {
			c.out = wire.AppendLiteral(c.out, c.buf[c.lit:c.pos])
		}
		c.out = wire.AppendBackref(c.out, dist, l)
		c.pos += l
		c.lit = c.pos
	}
	if final {
		c.pos = len(c.buf)
		if c.lit < c.pos {
			c.out = wire.AppendLiteral(c.out, c.buf[c.lit:c.pos])
		}
	}
}

type Encoder struct {
	w             io.Writer
	core          *core
	clean, closed bool
	err           error
}

func New(w io.Writer, cfg Config) (*Encoder, error) {
	c, err := newCore(cfg, nil, 0)
	if err != nil {
		return nil, err
	}
	c.out = wire.Header()
	return &Encoder{w: w, core: c, clean: true}, nil
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	e.core.buf = append(e.core.buf, p...)
	e.clean = e.clean && len(p) == 0
	e.core.process(false)
	e.drain()
	return len(p), e.err
}

// Flush 强制结束当前匹配并输出刷新标记；无新输入时输出零字节。
func (e *Encoder) Flush() error {
	if !e.clean {
		e.core.process(true)
		e.core.out = wire.AppendFlush(e.core.out)
		e.clean = true
		e.drain()
	}
	return e.err
}

func (e *Encoder) Close() error {
	if e.closed {
		return e.err
	}
	e.core.process(true)
	e.core.out = wire.AppendEnd(e.core.out, uint64(len(e.core.buf)), wire.Checksum(e.core.buf))
	e.closed, e.clean = true, true
	e.drain()
	return e.err
}

func (e *Encoder) Candidates() int64 { return e.core.m.Candidates() }

func (e *Encoder) drain() {
	if len(e.core.out) > 0 && e.err == nil {
		_, e.err = e.w.Write(e.core.out)
		e.core.out = e.core.out[:0]
	}
}

// CompressParallel 按块并发压缩；输出是与 worker 数无关的合法单流。
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrInvalidBlock
	}
	if _, err := newCore(cfg, nil, 0); err != nil {
		return nil, err
	}
	outs := make([][]byte, (len(data)+blockSize-1)/blockSize)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Go(func() {
			for i := w; i < len(outs); i += workers {
				lo, hi := i*blockSize, min((i+1)*blockSize, len(data))
				dict := max(lo-cfg.Window, 0)
				c, _ := newCore(cfg, data[dict:hi], lo-dict)
				c.process(true)
				outs[i] = c.out
			}
		})
	}
	wg.Wait()
	out := append(wire.Header(), slices.Concat(outs...)...)
	return wire.AppendEnd(out, uint64(len(data)), wire.Checksum(data)), nil
}
