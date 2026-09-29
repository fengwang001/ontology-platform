// Package split 把字节流切成内容定义的块。
//
// 滚动哈希窗口跨整个流连续推进、永不重置（DESIGN.md 第 1 节，写法乙）：
// 当前块起点之后的前 min 个字节照常进窗口，只是不允许判定边界；走到 max 仍无
// 内容边界则强制切；Flush 时剩余字节（即使短于 min）单独成尾块。
package split

import (
	"errors"

	"ontology/roll"
)

var (
	// ErrBadRange 在 min>max 或 min<=0 时返回。
	ErrBadRange = errors.New("split: require 0 < min <= max")
	// ErrBadWindow 在窗口长度为 0 或大于 min 时返回（与 roll.ErrBadWindow 不同，
	// 后者表示宽度本身非法；此处表示窗口与 min 的关系非法）。
	ErrBadWindow = errors.New("split: require 0 < window <= min")
	// ErrFlushed 在 Flush 之后再 Write 时返回。
	ErrFlushed = errors.New("split: chunker already flushed")
)

// Config 描述分块参数。
type Config struct {
	Min    int // 最小块长（尾块除外）
	Max    int // 最大块长，到达即强制切
	Window int // 滚动哈希窗口宽度，必须 <= Min
	Bits   int // 谓词低 Bits 位全 0；<=0 用默认
}

// Chunk 是一块的输出描述。Start/End 是其在完整字节流中的绝对偏移 [Start,End)。
type Chunk struct {
	Start int64
	End   int64
	Data  []byte
}

// Chunker 是有状态的流式分块器，非并发安全（由上层加锁）。
type Chunker struct {
	cfg      Config
	mask     uint64
	rh       *roll.Hasher
	buf      []byte // 当前块（从流起点起累计的未输出字节）
	off      int64  // 已输出字节数（= 下一块起点）
	done     bool
}

// New 校验参数并构造分块器。
func New(cfg Config) (*Chunker, error) {
	if cfg.Min <= 0 || cfg.Min > cfg.Max {
		return nil, ErrBadRange
	}
	if cfg.Window <= 0 || cfg.Window > cfg.Min {
		return nil, ErrBadWindow
	}
	rh, err := roll.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	return &Chunker{cfg: cfg, rh: rh, mask: roll.Mask(cfg.Bits)}, nil
}

// Write 追加字节，返回本次新完成的块（可能为空）。空 p 合法且无副作用。
func (c *Chunker) Write(p []byte) ([]Chunk, error) {
	if c.done {
		return nil, ErrFlushed
	}
	if len(p) == 0 {
		return nil, nil
	}
	var out []Chunk
	for _, b := range p {
		c.rh.Push(b)
		c.buf = append(c.buf, b)
		n := len(c.buf)
		content := n >= c.cfg.Min && c.rh.Full() && c.rh.Sum()&c.mask == 0
		forced := n >= c.cfg.Max
		if content || forced {
			out = append(out, c.emit())
		}
	}
	return out, nil
}

func (c *Chunker) emit() Chunk {
	data := make([]byte, len(c.buf))
	copy(data, c.buf)
	ch := Chunk{Start: c.off, End: c.off + int64(len(data)), Data: data}
	c.off = ch.End
	c.buf = c.buf[:0]
	return ch
}

// Flush 结束流：剩余字节（含短于 min 的尾块）单独成块；空流返回空切片。
// 多次 Flush 返回 ErrFlushed。
func (c *Chunker) Flush() ([]Chunk, error) {
	if c.done {
		return nil, ErrFlushed
	}
	c.done = true
	if len(c.buf) == 0 {
		return nil, nil
	}
	return []Chunk{c.emit()}, nil
}

// Segment 是一次性便捷分块，语义等价于 New + Write(全部) + Flush。
func Segment(cfg Config, data []byte) ([]Chunk, error) {
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	out, err := c.Write(data)
	if err != nil {
		return nil, err
	}
	tail, err := c.Flush()
	return append(out, tail...), err
}

// advances 供同模块（stream 包内测试经 stream 转发）读取滚动哈希推进次数；
// 该数字不是分块器公开接口的一部分。
func (c *Chunker) advances() uint64 { return c.rh.advancesCount() }
