package enc

import (
	"errors"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 15
	DefaultChain  = 64
)

type Config struct {
	WindowCap int
	MaxChain  int
}

// Compressor 是流式压缩器；单个实例不是并发安全的。
type Compressor struct {
	cfg     Config
	m       *match.Matcher
	out     []byte
	pending []byte // 未决输入，对应绝对区间 [base,base+len)
	base    int64
	hash    uint64
	closed  bool
	dirty   bool // 自上次 Flush/起始是否有新输入
}

func newComp(cfg Config, dict []byte, head bool) (*Compressor, error) {
	if cfg.WindowCap <= 0 {
		cfg.WindowCap = DefaultWindow
	}
	if cfg.MaxChain <= 0 {
		cfg.MaxChain = DefaultChain
	}
	w, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	m, err := match.New(w, cfg.MaxChain, dict)
	if err != nil {
		return nil, err
	}
	c := &Compressor{cfg: cfg, m: m, hash: wire.FNVOffset}
	if head {
		c.out = []byte{'O', 'N', 'T'}
		c.out = wire.PutVarint(c.out, uint64(wire.Version))
		c.out = wire.PutVarint(c.out, uint64(cfg.WindowCap))
	}
	return c, nil
}

// New 创建压缩器并写流头。
func New(cfg Config) (*Compressor, error) { return newComp(cfg, nil, true) }

// Write 喂入原文；压缩字节通过 Bytes 取走。
func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: write after close")
	}
	if len(p) == 0 {
		return 0, nil
	}
	c.hash = wire.FNV(c.hash, p)
	c.m.Append(p)
	c.pending = append(c.pending, p...)
	c.dirty = true
	for len(c.pending) >= c.cfg.WindowCap {
		c.settle(c.cfg.WindowCap)
	}
	return len(p), nil
}

func (c *Compressor) settle(k int) {
	lit := 0
	emit := func(end int) {
		if end > lit {
			c.out = wire.PutTagged(c.out, wire.TagLit, uint64(end-lit))
			c.out = append(c.out, c.pending[lit:end]...)
		}
	}
	i := 0
	for i < k {
		dist, ln := c.m.Find(c.base+int64(i), c.base+int64(k))
		if ln == 0 {
			i++
			continue
		}
		emit(i)
		c.out = wire.PutTagged(c.out, wire.TagRef, uint64(dist))
		c.out = wire.PutVarint(c.out, uint64(ln))
		i += ln
		lit = i
	}
	emit(k)
	c.pending = c.pending[k:]
	c.base += int64(k)
}

// Flush 强制结束当前可能的匹配并写刷新标记；无新输入时不产生字节。
func (c *Compressor) Flush() error {
	if c.closed {
		return errors.New("enc: flush after close")
	}
	if !c.dirty {
		return nil
	}
	c.settle(len(c.pending))
	c.out = append(c.out, wire.TagFlush)
	c.dirty = false
	return nil
}

// Close 结算全部输入并写流尾。
func (c *Compressor) Close() error {
	if c.closed {
		return errors.New("enc: closed twice")
	}
	c.settle(len(c.pending))
	c.closed = true
	c.out = append(c.out, wire.TagEnd)
	c.out = wire.PutVarint(c.out, uint64(c.base))
	c.out = wire.PutVarint(c.out, c.hash)
	return nil
}

func (c *Compressor) Bytes() []byte {
	b := c.out
	c.out = nil
	return b
}

// Compress 一次性压缩 data。
func Compress(data []byte, cfg Config) ([]byte, error) {
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write(data); err != nil {
		return nil, err
	}
	if err := c.Close(); err != nil {
		return nil, err
	}
	return c.Bytes(), nil
}
