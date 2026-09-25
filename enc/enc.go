// Package enc 实现流式 LZ77 压缩器（Write/Flush/Close）与按块并行压缩。
// 单个 Compressor 实例不是并发安全的。依赖 wire、match、window。
package enc

import (
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// ErrClosed 表示在 Close 之后又调用了 Write/Flush。
var ErrClosed = errors.New("enc: compressor already closed")

// Config 压缩配置；零值字段取默认值。
type Config struct {
	Window   int // 滑动窗口容量，默认 1<<16
	MaxChain int // 候选链长度上限，默认 32
}

func (c Config) withDefaults() Config {
	if c.Window == 0 {
		c.Window = 1 << 16
	}
	if c.MaxChain == 0 {
		c.MaxChain = 32
	}
	return c
}

// Compressor 流式压缩器。token 序列只取决于原文与 Flush 偏移，与 Write 切法无关。
type Compressor struct {
	w        io.Writer
	m        *match.Matcher
	pos      int  // 下一个未决位置
	litStart int  // 挂起字面量段起点
	pending  bool // 自上次 Flush 后有新输入
	closed   bool
	err      error
}

// New 创建压缩器并立即写出流头；非法配置（窗口/链长 <=0）返回错误。
func New(w io.Writer, cfg Config) (*Compressor, error) {
	cfg = cfg.withDefaults()
	win, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, cfg.MaxChain)
	if err != nil {
		return nil, err
	}
	c := &Compressor{w: w, m: m}
	if _, err := w.Write(wire.Header()); err != nil {
		return nil, err
	}
	return c, nil
}

// Write 追加输入；只发出已终局的 token，未决字节缓存待后续数据或 Flush。
func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, ErrClosed
	}
	if c.err != nil {
		return 0, c.err
	}
	c.m.Append(p)
	if len(p) > 0 {
		c.pending = true
	}
	c.process(false)
	return len(p), c.err
}

// process 推进未决位置。final 为 true 时强制截断未决匹配并发出全部挂起字节。
func (c *Compressor) process(final bool) {
	data := c.m.Data()
	for c.pos+match.MinLen <= len(data) {
		dist, l := c.m.Find(c.pos)
		if l >= match.MinLen && (final || c.pos+l < len(data) || l == match.MaxLen) {
			c.emit(wire.AppendLiteral(nil, data[c.litStart:c.pos]))
			c.emit(wire.AppendBackref(nil, dist, l))
			c.pos += l
			c.litStart = c.pos
		} else if l >= match.MinLen {
			break // 匹配延伸到缓冲末尾，可能变长，暂不决定
		} else {
			c.pos++
		}
	}
	if final && c.litStart < len(data) {
		c.emit(wire.AppendLiteral(nil, data[c.litStart:]))
		c.pos, c.litStart = len(data), len(data)
	}
}

func (c *Compressor) emit(p []byte) {
	if c.err != nil || len(p) == 0 {
		return
	}
	_, c.err = c.w.Write(p)
}

// Flush 强制结束当前匹配并发出全部挂起字节，再加刷新标记；
// 自上次 Flush 无新输入时不产生任何字节。
func (c *Compressor) Flush() error {
	if c.closed {
		return ErrClosed
	}
	if !c.pending {
		return c.err
	}
	c.process(true)
	c.emit(wire.AppendFlush(nil))
	c.pending = false
	return c.err
}

// Close 发出剩余未决字节与流尾（总长 + 校验和）。幂等。
func (c *Compressor) Close() error {
	if c.closed {
		return c.err
	}
	c.process(true)
	c.emit(wire.AppendTrailer(nil, uint64(c.m.Len()), wire.Checksum(c.m.Data())))
	c.closed = true
	return c.err
}

// CompressParallel 按 blockSize 切块并发压缩；每块以上一块末尾窗口容量以内
// 的字节为预置字典。输出与 workers 无关，是合法的完整压缩流。
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be positive")
	}
	cfg := Config{}.withDefaults()
	n := (len(data) + blockSize - 1) / blockSize
	results := make([][]byte, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = compressBlock(data, i*blockSize, min((i+1)*blockSize, len(data)), cfg)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	out := wire.Header()
	for _, r := range results {
		out = append(out, r...)
	}
	return wire.AppendTrailer(out, uint64(len(data)), wire.Checksum(data)), nil
}

// compressBlock 压缩 data[start:end]，以 data[max(0,start-Window):start] 为字典。
func compressBlock(data []byte, start, end int, cfg Config) []byte {
	win, _ := window.New(cfg.Window)
	m, _ := match.New(win, cfg.MaxChain)
	dictStart := max(0, start-cfg.Window)
	m.Append(data[dictStart:end])
	base := start - dictStart
	var out []byte
	litStart, pos := base, base
	for pos < m.Len() {
		dist, l := m.Find(pos)
		if l >= match.MinLen {
			out = wire.AppendLiteral(out, m.Data()[litStart:pos])
			out = wire.AppendBackref(out, dist, l)
			pos += l
			litStart = pos
		} else {
			pos++
		}
	}
	return wire.AppendLiteral(out, m.Data()[litStart:])
}
