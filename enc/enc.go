// Package enc is the streaming LZ77 compressor plus deterministic
// parallel block compression. Depends on wire, match, window.
package enc

import (
	"errors"
	"hash/fnv"
	"io"
	"sync"

	"ontology/match"
	"ontology/wire"
)

// Config configures a Writer.
type Config struct {
	WindowCap int
	MaxChain  int
}

// Writer emits records only at Flush/Close, so compressed bytes are
// independent of Write chunking. Not safe for concurrent use.
type Writer struct {
	w       io.Writer
	m       *match.Matcher
	pending []byte
	total   int
	sum     uint64
	closed  bool
	hdrOut  bool
}

// NewWriter creates a compressor writing to w.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	m, err := match.New(match.Config(cfg))
	if err != nil {
		return nil, err
	}
	return &Writer{w: w, m: m, sum: 14695981039346656037}, nil
}

// Write appends input; no compressed bytes are emitted yet.
func (c *Writer) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: write after close")
	}
	for _, b := range p {
		c.sum ^= uint64(b)
		c.sum *= 1099511628211
	}
	c.pending = append(c.pending, p...)
	c.total += len(p)
	return len(p), nil
}

func emit(out []byte, recs []match.Record) []byte {
	for i := 0; i < len(recs); {
		if recs[i].Len != 1 {
			out = wire.AppendMatch(out, recs[i].Dist, recs[i].Len)
			i++
			continue
		}
		j := i
		for j < len(recs) && recs[j].Len == 1 {
			j++
		}
		lit := make([]byte, j-i)
		for k := range lit {
			lit[k] = recs[i+k].Lit
		}
		out, i = wire.AppendLiteral(out, lit), j
	}
	return out
}

// Flush makes every buffered byte decodable; an empty Flush emits none.
func (c *Writer) Flush() error {
	if c.closed {
		return errors.New("enc: flush after close")
	}
	if !c.hdrOut {
		if _, err := c.w.Write(wire.AppendHeader(nil)); err != nil {
			return err
		}
		c.hdrOut = true
	}
	if len(c.pending) == 0 {
		return nil
	}
	var recs []match.Record
	recs = c.m.Process(c.pending, recs)
	if _, err := c.w.Write(wire.AppendFlush(emit(nil, recs))); err != nil {
		return err
	}
	c.pending = c.pending[:0]
	return nil
}

// Close flushes remaining input and writes the trailer.
func (c *Writer) Close() error {
	if c.closed {
		return errors.New("enc: close twice")
	}
	c.closed = true
	if err := c.Flush(); err != nil {
		return err
	}
	_, err := c.w.Write(wire.AppendEnd(nil, c.total, c.sum))
	return err
}

func compressBlock(prev, block []byte, cfg match.Config) []byte {
	m, _ := match.New(cfg)
	m.Reset(prev)
	var recs []match.Record
	return emit(nil, m.Process(block, recs))
}

// CompressParallel splits data into fixed blockSize chunks; each block
// may reference up to WindowCap trailing bytes of the previous block.
// Output is independent of workers.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be > 0")
	}
	mcfg := match.Config(cfg)
	num := (len(data) + blockSize - 1) / blockSize
	if num == 0 {
		num = 1
	}
	if workers > num {
		workers = num
	}
	blocks := make([][]byte, num)
	jobs := make(chan int, num)
	for i := 0; i < num; i++ {
		jobs <- i
}
	close(jobs)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lo, hi := i*blockSize, (i+1)*blockSize
				if hi > len(data) {
					hi = len(data)
				}
				var prev []byte
				if i > 0 {
					prev = data[lo-blockSize : lo]
				}
				blocks[i] = compressBlock(prev, data[lo:hi], mcfg)
			}
		}()
	}
	wg.Wait()
	out := wire.AppendHeader(nil)
	for _, b := range blocks {
		out = append(out, b...)
	}
	return wire.AppendEnd(out, len(data), wire.Checksum(data)), nil
}
