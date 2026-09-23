package enc

import (
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/wire"
)

var (
	ErrConfig    = errors.New("lz77: invalid compressor configuration")
	ErrBlockSize = errors.New("lz77: block size must cover window capacity")
	ErrWorkers   = errors.New("lz77: worker count must be positive")
	ErrClosed    = errors.New("lz77: compressor closed")
)

type Config struct {
	WindowCapacity int
	MaxChain       int
}

type Compressor struct {
	w       io.Writer
	cfg     match.Config
	pending []byte
	flushed int
	started bool
	closed  bool
	sum     uint64
}

func New(w io.Writer, c Config) (*Compressor, error) {
	cfg := match.Config{WindowCapacity: c.WindowCapacity, MaxChain: c.MaxChain}
	if c.WindowCapacity == 0 {
		cfg.WindowCapacity = match.DefaultCapacity
	}
	if c.MaxChain == 0 {
		cfg.MaxChain = match.DefaultMaxChain
	}
	if _, err := match.New(cfg); err != nil {
		return nil, err
	}
	return &Compressor{w: w, cfg: cfg, sum: 14695981039346656037}, nil
}

func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, ErrClosed
	}
	c.pending = append(c.pending, p...)
	return len(p), nil
}

func (c *Compressor) Flush() error {
	if c.closed {
		return ErrClosed
	}
	if len(c.pending) == 0 {
		return nil
	}
	if !c.started {
		if err := wire.Header(c.w); err != nil {
			return err
		}
		c.started = true
	}
	segment := c.pending
	c.pending = nil
	c.flushed += len(segment)
	if err := writeBlock(c.w, c.cfg, nil, segment); err != nil {
		return err
	}
	c.sum = fnvAdd(c.sum, segment)
	return wire.Tag(c.w, wire.TagFlush)
}

func (c *Compressor) Close() error {
	if c.closed {
		return ErrClosed
	}
	c.closed = true
	if !c.started {
		if err := wire.Header(c.w); err != nil {
			return err
		}
		c.started = true
	}
	if len(c.pending) > 0 {
		segment := c.pending
		c.pending = nil
		c.flushed += len(segment)
		if err := writeBlock(c.w, c.cfg, nil, segment); err != nil {
			return err
		}
		c.sum = fnvAdd(c.sum, segment)
	}
	if err := wire.Tag(c.w, wire.TagEnd); err != nil {
		return err
	}
	var b [10]byte
	n := putUvarint(b[:], uint64(c.flushed))
	if _, err := c.w.Write(b[:n]); err != nil {
		return err
	}
	n = putUvarint(b[:], c.sum)
	_, err := c.w.Write(b[:n])
	return err
}

func writeBlock(w io.Writer, cfg match.Config, preset, data []byte) error {
	if err := wire.Tag(w, wire.TagBlock); err != nil {
		return err
	}
	m, err := match.New(cfg)
	if err != nil {
		return err
	}
	if err := m.Preset(preset); err != nil {
		return err
	}
	lit := 0
	emit := func(end int) error {
		if lit > 0 {
			if err := wire.Literal(w, data[end-lit:end]); err != nil {
				return err
			}
			lit = 0
		}
		return nil
	}
	pos := 0
	for pos < len(data) {
		d, n := m.Find(data, pos)
		if n < 3 {
			lit++
			m.Add(data[pos : pos+1])
			pos++
			continue
		}
		if err := emit(pos); err != nil {
			return err
		}
		if err := wire.Match(w, d, n); err != nil {
			return err
		}
		m.Add(data[pos : pos+n])
		pos += n
	}
	return emit(len(data))
}

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	cfg := match.Config{WindowCapacity: match.DefaultCapacity, MaxChain: match.DefaultMaxChain}
	if blockSize == 0 {
		blockSize = 64 * 1024
	}
	if workers <= 0 {
		return nil, ErrWorkers
	}
	if blockSize < cfg.WindowCapacity {
		return nil, ErrBlockSize
	}
	blocks := make([][]byte, 0, (len(data)+blockSize-1)/blockSize+1)
	for start := 0; start < len(data) || start == 0; start += blockSize {
		blocks = append(blocks, data[start:min(len(data), start+blockSize)])
	}
	encoded := make([][]byte, len(blocks))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				preset := []byte(nil)
				start := j * blockSize
				if j > 0 {
					from := max(0, start-cfg.WindowCapacity)
					preset = data[from:start]
				}
				var b byteBuffer
				_ = writeBlock(&b, cfg, preset, blocks[j])
				encoded[j] = b
			}
		}()
	}
	for i := range blocks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var out byteBuffer
	_ = wire.Header(&out)
	sum := uint64(14695981039346656037)
	for i, block := range blocks {
		_ = i
		out.Write(encoded[i])
		sum = fnvAdd(sum, block)
	}
	wire.Tag(&out, wire.TagEnd)
	out.buf = wire.AppendUvarint(out.buf, uint64(len(data)))
	out.buf = wire.AppendUvarint(out.buf, sum)
	return out.buf, nil
}

type byteBuffer struct{ buf []byte }

func (b *byteBuffer) Write(p []byte) (int, error) { b.buf = append(b.buf, p...); return len(p), nil }

func fnvAdd(sum uint64, p []byte) uint64 {
	if sum == 0 {
		sum = 14695981039346656037
	}
	for _, b := range p {
		sum ^= uint64(b)
		sum *= 1099511628211
	}
	return sum
}

func putUvarint(p []byte, v uint64) int {
	n := 0
	for v >= 0x80 {
		p[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	p[n] = byte(v)
	return n + 1
}
