// Package chunker 实现只依赖字节流与注入时钟的切块策略：最小块聚合、
// 最大块切分、时间窗收尾。块序列与上游的 Write 调用边界无关。
package chunker

import (
	"errors"
	"fmt"
	"time"
)

// ErrClosed 表示在 Close 之后继续写入。
var ErrClosed = errors.New("chunker: write after close")

// Clock 是注入时钟，所有时间判定只经由它。
type Clock interface {
	Now() time.Time
}

// Ext 是附加到每个数据块的扩展键值对。
type Ext struct {
	Key   string
	Value string
}

// Chunk 是一个切好的块。
type Chunk struct {
	Data []byte
	Exts []Ext
}

// Config 配置切块策略。
type Config struct {
	MinChunk int           // 聚合下限：窗口到期时 pending 达到它才吐块
	MaxChunk int           // 满块大小：达到立即吐块，单次大写入被切分
	Window   time.Duration // 聚合时间窗
	Exts     []Ext         // 附加到每个块的扩展
	Clock    Clock         // 注入时钟
}

// State 是切块器的可恢复快照。
type State struct {
	Pending     []byte
	WindowStart time.Time
	HasWindow   bool
	Closed      bool
}

// Chunker 按策略把字节流切成块。
type Chunker struct {
	cfg      Config
	pending  []byte
	winStart time.Time
	hasWin   bool
	closed   bool
}

// New 校验配置并创建切块器。
func New(cfg Config) (*Chunker, error) {
	if cfg.Clock == nil {
		return nil, errors.New("chunker: clock is required")
	}
	if cfg.MinChunk <= 0 || cfg.MaxChunk < cfg.MinChunk {
		return nil, fmt.Errorf("chunker: require 0 < MinChunk <= MaxChunk (got %d,%d)",
			cfg.MinChunk, cfg.MaxChunk)
	}
	if cfg.Window < 0 {
		return nil, errors.New("chunker: window must be >= 0")
	}
	return &Chunker{cfg: cfg}, nil
}

// Write 追加字节，返回本次凑满 MaxChunk 而切出的块；零长写入不产块。
func (c *Chunker) Write(p []byte) ([]Chunk, error) {
	if c.closed {
		return nil, ErrClosed
	}
	if len(p) == 0 {
		return nil, nil
	}
	if !c.hasWin {
		c.winStart = c.cfg.Clock.Now()
		c.hasWin = true
	}
	c.pending = append(c.pending, p...)
	var out []Chunk
	for len(c.pending) >= c.cfg.MaxChunk {
		buf := make([]byte, c.cfg.MaxChunk)
		copy(buf, c.pending[:c.cfg.MaxChunk])
		out = append(out, Chunk{Data: buf, Exts: c.cfg.Exts})
		c.pending = c.pending[c.cfg.MaxChunk:]
	}
	if len(c.pending) == 0 {
		c.hasWin = false
	}
	return out, nil
}

// Tick 在时钟推进后调用：时间窗到期且聚合量达到 MinChunk 时吐出一个块。
func (c *Chunker) Tick() []Chunk {
	if c.closed || !c.hasWin || len(c.pending) == 0 {
		return nil
	}
	if c.cfg.Clock.Now().Sub(c.winStart) < c.cfg.Window || len(c.pending) < c.cfg.MinChunk {
		return nil
	}
	ch := Chunk{Data: c.pending, Exts: c.cfg.Exts}
	c.pending = nil
	c.hasWin = false
	return []Chunk{ch}
}

// Close 吐出尾部剩余字节；重复调用幂等。
func (c *Chunker) Close() []Chunk {
	if c.closed {
		return nil
	}
	c.closed = true
	if len(c.pending) == 0 {
		return nil
	}
	ch := Chunk{Data: c.pending, Exts: c.cfg.Exts}
	c.pending = nil
	c.hasWin = false
	return []Chunk{ch}
}

// Save 返回内部状态快照。
func (c *Chunker) Save() State {
	return State{
		Pending:     append([]byte(nil), c.pending...),
		WindowStart: c.winStart,
		HasWindow:   c.hasWin,
		Closed:      c.closed,
	}
}

// Restore 把内部状态恢复到快照。
func (c *Chunker) Restore(s State) {
	c.pending = append([]byte(nil), s.Pending...)
	c.winStart = s.WindowStart
	c.hasWin = s.HasWindow
	c.closed = s.Closed
}
