// Package enc 提供流式压缩器与按块并行压缩。
// 单个 Encoder 不是并发安全的，Close 之后不得再写。
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

// Config 是压缩配置；WindowCap 与 MaxChain 必须为正。
type Config struct{ WindowCap, MaxChain int }

// core 是编码核心：窗口 + 匹配器 + 未决字节缓冲。
type core struct {
	win           *window.Window
	m             *match.Matcher
	out           io.Writer
	pending, lits []byte // 未决字节（含开放匹配）、待发出的字面量段
	pos           uint64 // pending[0] 的绝对位置（含预置字典）
	total, sum    uint64 // 原文字节数（不含字典）与滚动校验和
	err           error
}

func newCore(cfg Config, dict []byte, out io.Writer) (*core, error) {
	if cfg.WindowCap <= 0 {
		return nil, window.ErrZeroCapacity
	}
	if cfg.MaxChain <= 0 {
		return nil, match.ErrZeroChain
	}
	w, _ := window.New(cfg.WindowCap)
	m, _ := match.New(w, cfg.MaxChain)
	c := &core{win: w, m: m, out: out, sum: wire.SumSeed, pos: uint64(len(dict))}
	for _, b := range dict {
		w.Append(b)
	}
	return c, nil
}

// src 返回绝对位置 q 的字节：q < pos 在窗口，否则在 pending。
func (c *core) src(q uint64) byte {
	if q < c.pos {
		return c.win.Byte(int(c.pos - q))
	}
	return c.pending[q-c.pos]
}

func (c *core) write(p []byte) {
	if c.err == nil {
		_, c.err = c.out.Write(p)
	}
}

func (c *core) flushLits() {
	if len(c.lits) > 0 {
		c.write(wire.AppendLiteral(nil, c.lits))
		c.lits = c.lits[:0]
	}
}

// advance 消费 pending 前 k 个字节：入窗、滚动校验和。
func (c *core) advance(k int) {
	for _, b := range c.pending[:k] {
		c.win.Append(b)
		c.sum = wire.SumByte(c.sum, b)
	}
	c.total, c.pos, c.pending = c.total+uint64(k), c.pos+uint64(k), c.pending[k:]
}

// process 编码所有可决定的字节；final 为 true 时强制闭合开放匹配。
func (c *core) process(final bool) {
	for len(c.pending) > 0 {
		dist, l := 0, 0
		if len(c.pending) >= match.MinMatch {
			dist, l = c.m.FindLongest(c.src, c.pos, c.pending)
		} else if !final {
			return // 未决字节不足，无法判定字面量还是匹配
		}
		if l < match.MinMatch {
			c.lits = append(c.lits, c.pending[0])
			c.advance(1)
			continue
		}
		if l == len(c.pending) && !final {
			return // 开放匹配：到达缓冲末尾，等待更多输入
		}
		c.flushLits()
		c.write(wire.AppendBackref(nil, dist, l))
		c.advance(l)
	}
}

// Encoder 是流式压缩器。Write 边界不影响输出；Flush 强制闭合开放匹配。
type Encoder struct{ core *core }

// New 构造压缩器并立即写出流头；非法配置（窗口或链长为 0）被拒绝。
func New(out io.Writer, cfg Config) (*Encoder, error) {
	c, err := newCore(cfg, nil, out)
	if err != nil {
		return nil, err
	}
	c.write(wire.Header())
	return &Encoder{core: c}, nil
}

// Write 吸收输入；返回错误来自底层 writer。
func (e *Encoder) Write(p []byte) (int, error) {
	e.core.pending = append(e.core.pending, p...)
	e.core.process(false)
	return len(p), e.core.err
}

// Flush 闭合开放匹配并发出全部未决字节；无新输入时不产生任何字节。
func (e *Encoder) Flush() error {
	if len(e.core.pending) == 0 && len(e.core.lits) == 0 {
		return e.core.err
	}
	e.core.process(true)
	e.core.flushLits()
	e.core.write(wire.AppendFlush(nil))
	return e.core.err
}

// Close 发出全部未决字节与流尾。
func (e *Encoder) Close() error {
	e.core.process(true)
	e.core.flushLits()
	e.core.write(wire.AppendEnd(nil, e.core.total, e.core.sum))
	return e.core.err
}

// CompressParallel 按 blockSize 切块并发压缩，每块以上一块末尾
// 窗口容量以内的原文为预置字典；结果与 workers 无关，逐字节确定。
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	cfg := Config{WindowCap: 1 << 15, MaxChain: 32}
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be positive")
	}
	recs := make([][]byte, (len(data)+blockSize-1)/blockSize)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < len(recs); i += workers {
				s := i * blockSize
				var buf bytes.Buffer
				c, _ := newCore(cfg, data[max(0, s-cfg.WindowCap):s], &buf)
				c.pending = append(c.pending, data[s:min(s+blockSize, len(data))]...)
				c.process(true)
				c.flushLits()
				recs[i] = buf.Bytes()
			}
		}()
	}
	wg.Wait()
	out := wire.Header()
	for _, r := range recs {
		out = append(out, r...)
	}
	return wire.AppendEnd(out, uint64(len(data)), wire.SumOf(data)), nil
}
