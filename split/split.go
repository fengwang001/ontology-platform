// Package split divides byte streams at content-defined boundaries.
package split

import (
	"errors"

	"ontology/roll"
)

// Config configures minimum, maximum and average chunk sizes.
type Config struct {
	Min    int
	Max    int
	Window int
	Target uint16
}

// Chunk is a byte range in the original stream.
type Chunk struct {
	Start int64
	End   int64
	Data  []byte
}

// Chunker is independent of how writes are divided.
type Chunker struct {
	cfg  Config
	rh   *roll.Hasher
	buf  []byte
	base int64
}

var (
	// ErrInvalidRange means Min, Max, or their relationship is invalid.
	ErrInvalidRange = errors.New("split: invalid chunk size range")
	// ErrInvalidWindow means zero or larger-than-Min window.
	ErrInvalidWindow = errors.New("split: invalid rolling window")
)

// New validates configuration and returns a chunker.
func New(cfg Config) (*Chunker, error) {
	if cfg.Min <= 0 || cfg.Max < cfg.Min || cfg.Max < 2*cfg.Min-1 {
		return nil, ErrInvalidRange
	}
	if cfg.Window <= 0 || cfg.Window > cfg.Min {
		return nil, ErrInvalidWindow
	}
	if cfg.Target == 0 {
		cfg.Target = 1
	}
	rh, err := roll.New(cfg.Window)
	if err != nil {
		return nil, ErrInvalidWindow
	}
	return &Chunker{cfg: cfg, rh: rh}, nil
}

// Feed appends bytes without changing how boundaries are selected.
func (c *Chunker) Feed(p []byte) {
	c.buf = append(c.buf, p...)
}

// Flush returns all chunks and resets the stream position.
func (c *Chunker) Flush() []Chunk {
	data := c.buf
	out := c.cut(data)
	c.buf = c.buf[:0]
	c.base = 0
	return out
}

func (c *Chunker) cut(data []byte) []Chunk {
	if len(data) == 0 {
		return nil
	}
	chunks, start := make([]Chunk, 0, len(data)/c.cfg.Min+1), 0
	for pos := range data {
		n := pos + 1 - start
		if n < c.cfg.Window {
			c.rh.Add(data[pos])
		} else if n == c.cfg.Window {
			for i := 0; i < c.cfg.Window; i++ {
				c.rh.Add(data[start+i])
			}
		} else {
			c.rh.Advance(data[pos-c.cfg.Window], data[pos])
		}
		boundary := n >= c.cfg.Min && n <= c.cfg.Max &&
			(n == c.cfg.Max || (n >= c.cfg.Window && c.rh.Value()%c.cfg.Target == 0))
		if boundary {
			chunks = append(chunks, Chunk{int64(start), int64(pos + 1), append([]byte(nil), data[start:pos+1]...)})
			start = pos + 1
			c.rh.Reset()
		}
	}
	return c.finish(data, chunks, start)
}

func (c *Chunker) finish(data []byte, chunks []Chunk, start int) []Chunk {
	if start == len(data) {
		return chunks
	}
	tail := data[start:]
	if len(tail) >= c.cfg.Min || len(chunks) == 0 {
		return append(chunks, Chunk{int64(start), int64(len(data)), append([]byte(nil), tail...)})
	}
	last := &chunks[len(chunks)-1]
	merged := append(append([]byte(nil), last.Data...), tail...)
	if len(merged) <= c.cfg.Max {
		last.End, last.Data = int64(len(data)), merged
		return chunks
	}
	cut := len(merged) / 2
	if cut < c.cfg.Min {
		cut = c.cfg.Min
	}
	if len(merged)-cut < c.cfg.Min || cut > c.cfg.Max {
		cut = len(merged) - c.cfg.Min
	}
	last.End, last.Data = last.Start+int64(cut), append([]byte(nil), merged[:cut]...)
	return append(chunks, Chunk{last.End, int64(len(data)), append([]byte(nil), merged[cut:]...)})
}
