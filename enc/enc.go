// Package enc is the streaming and parallel LZ77 compressor.
// A single Compressor is not safe for concurrent use.
package enc

import (
	"errors"
	"hash/crc32"

	"ontology/match"
	"ontology/wire"
)

// Default tuning.
const (
	DefaultWindow = 1 << 15 // 32 KiB
	DefaultChain  = 64
)

// Config configures a Compressor.
type Config struct {
	Window int
	Chain  int
}

// Compressor writes a compressed stream to its output buffer.
type Compressor struct {
	cfg    Config
	m      *match.Matcher
	out    []byte
	pend   []byte // uncommitted bytes since the last segment end
	crc    uint32
	total  uint64
	dirty  bool // new bytes since last Flush
	hdr    bool
	closed bool
}

var errConfig = errors.New("enc: Window and Chain must be positive")

// New constructs a compressor with the given configuration.
func New(cfg Config) (*Compressor, error) {
	if cfg.Window <= 0 || cfg.Chain <= 0 {
		return nil, errConfig
	}
	return &Compressor{cfg: cfg, m: match.New(cfg.Window, cfg.Chain)}, nil
}

func (c *Compressor) ensureHeader() {
	if !c.hdr {
		c.out = append(c.out, wire.Header(uint64(c.cfg.Window))...)
		c.hdr = true
	}
}

// Write buffers input. Internally it seals segments of Window bytes so that a
// match is never cut at a Write boundary; output is independent of chunking.
func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: write after close")
	}
	c.pend = append(c.pend, p...)
	for len(c.pend) >= c.cfg.Window {
		c.compress(c.pend[:c.cfg.Window])
		c.pend = c.pend[c.cfg.Window:]
	}
	c.dirty = c.dirty || len(p) > 0
	return len(p), nil
}

// compress scans one sealed segment and emits records.
func (c *Compressor) compress(seg []byte) {
	c.ensureHeader()
	c.crc = crc32.Update(c.crc, crc32.IEEETable, seg)
	c.total += uint64(len(seg))
	litStart := 0
	emitLit := func(end int) {
		if end > litStart {
			c.out = append(c.out, wire.Literal(uint64(end-litStart))...)
			c.out = append(c.out, seg[litStart:end]...)
		}
	}
	for i := 0; i < len(seg); {
		if i+wire.MinMatch > len(seg) {
			break
		}
		if d, l := c.m.Find(i, seg); l >= wire.MinMatch {
			emitLit(i)
			c.out = append(c.out, wire.Match(uint64(d), uint64(l))...)
			for k := 1; k < l; k++ {
				if i+k+2 < len(seg) {
					c.m.Insert(seg[i+k], seg[i+k+1], seg[i+k+2])
				} else {
					c.m.Insert(seg[i+k], 0, 0)
				}
			}
			i += l
			litStart = i
			continue
		}
		i++
	}
	emitLit(len(seg))
	for ; i < len(seg); i++ {
		var b1, b2 byte
		if i+1 < len(seg) {
			b1 = seg[i+1]
		}
		if i+2 < len(seg) {
			b2 = seg[i+2]
		}
		c.m.Insert(seg[i], b1, b2)
	}
}

// Flush seals all buffered input and emits a flush marker. A second Flush with
// no new input emits nothing.
func (c *Compressor) Flush() error {
	if c.closed {
		return errors.New("enc: flush after close")
	}
	if len(c.pend) > 0 {
		c.compress(c.pend)
		c.pend = c.pend[:0]
	}
	if c.dirty {
		c.ensureHeader()
		c.out = append(c.out, wire.Flush()...)
		c.dirty = false
	}
	return nil
}

// Close seals remaining input and writes the trailer.
func (c *Compressor) Close() error {
	if c.closed {
		return errors.New("enc: close twice")
	}
	c.closed = true
	if len(c.pend) > 0 {
		c.compress(c.pend)
		c.pend = c.pend[:0]
	}
	c.ensureHeader() // empty input is still a valid non-empty stream
	c.out = append(c.out, wire.End(c.total, uint64(c.crc))...)
	return nil
}

// Bytes returns all compressed output produced so far.
func (c *Compressor) Bytes() []byte { return c.out }
