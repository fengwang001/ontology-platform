// Package enc is the streaming LZ77 compressor, plus deterministic parallel
// block compression. A single Compressor is not safe for concurrent use.
package enc

import (
	"errors"
	"hash/crc32"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// MaxMatch is the longest back-reference the format carries.
const MaxMatch = 1 << 20

// Options configures a compressor.
type Options struct {
	WindowCap int // 0 means 1<<16
	Chain     int // 0 means 64
}

// Compressor streams compressed bytes.
type Compressor struct {
	out   []byte
	buf   []byte
	win   *window.Window
	m     *match.Matcher
	pos   int
	total uint64
	crc   uint32
	closed bool
}

// New returns a streaming compressor.
func New(opts Options) (*Compressor, error) {
	wc, ch := opts.WindowCap, opts.Chain
	if wc <= 0 {
		wc = 1 << 16
	}
	if ch <= 0 {
		ch = 64
	}
	win, err := window.New(wc)
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, MaxMatch, ch)
	if err != nil {
		return nil, err
	}
	c := &Compressor{win: win, m: m, crc: crc32.NewIEEE().Sum32()}
	c.out = wire.Header(uint64(wc))
	return c, nil
}

// Write accepts uncompressed input.
func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: write after close")
	}
	c.buf = append(c.buf, p...)
	c.total += uint64(len(p))
	c.crc = crc32.Update(c.crc, crc32.IEEETable, p)
	for len(c.buf) > MaxMatch {
		c.encode(false)
	}
	return len(p), nil
}

// Flush forces all buffered input to be decodable.
func (c *Compressor) Flush() error {
	if c.closed {
		return errors.New("enc: flush after close")
	}
	for len(c.buf) > 0 {
		c.encode(true)
	}
	c.out = wire.Flush(c.out)
	return nil
}

// Close emits the stream tail.
func (c *Compressor) Close() error {
	for len(c.buf) > 0 {
		c.encode(true)
	}
	c.out = wire.End(c.out, c.total, uint64(c.crc))
	c.closed = true
	return nil
}

// Bytes returns the compressed output produced so far.
func (c *Compressor) Bytes() []byte { return c.out }

func (c *Compressor) encode(force bool) {
	cur := c.buf
	if !force {
		cur = cur[:len(cur)-MaxMatch] // keep holdback untouched
	}
	d, l := c.m.Find(c.pos, cur)
	if l >= match.MinMatch {
		c.out = wire.Match(c.out, d, l)
		for i := 0; i < l; i++ {
			c.m.Insert(c.pos+i, cur[i])
		}
		c.pos += l
		c.buf = c.buf[l:]
		return
	}
	c.out = wire.Literal(c.out, cur[:1])
	c.m.Insert(c.pos, cur[0])
	c.pos++
	c.buf = c.buf[1:]
}

// CompressParallel compresses data in fixed blocks using up to workers
// goroutines. Each block may back-reference the tail of the previous block
// (up to one window of dictionary). Output is identical for any workers>=1.
func CompressParallel(data []byte, blockSize, workers int, opts Options) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: bad parallel config")
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	blocks := make([][]byte, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				start := i * blockSize
				end := start + blockSize
				if end > len(data) {
					end = len(data)
				}
				dStart := start - opts.dictCap()
				if dStart < 0 {
					dStart = 0
				}
				blocks[i] = compressBlock(data[dStart:start], data[start:end], opts)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	wc := opts.WindowCap
	if wc <= 0 {
		wc = 1 << 16
	}
	out := wire.Header(uint64(wc))
	for _, b := range blocks {
		out = append(out, b...)
	}
	return wire.End(out, uint64(len(data)), uint64(crc32.ChecksumIEEE(data))), nil
}

func (o Options) dictCap() int {
	if o.WindowCap > 0 {
		return o.WindowCap
	}
	return 1 << 16
}

// compressBlock compresses one block seeded with an explicit dictionary.
// It emits only records (no header/tail), forcing the whole block out.
func compressBlock(dict, block []byte, opts Options) []byte {
	wc, ch := opts.WindowCap, opts.Chain
	if wc <= 0 {
		wc = 1 << 16
	}
	if ch <= 0 {
		ch = 64
	}
	win, _ := window.New(wc)
	win.PushN(dict)
	m, _ := match.New(win, MaxMatch, ch)
	var out []byte
	buf := append([]byte(nil), block...)
	pos := len(dict)
	for len(buf) > 0 {
		d, l := m.Find(pos, buf)
		if l >= match.MinMatch {
			out = wire.Match(out, d, l)
			for i := 0; i < l; i++ {
				m.Insert(pos+i, buf[i])
			}
			pos += l
			buf = buf[l:]
			continue
		}
		out = wire.Literal(out, buf[:1])
		m.Insert(pos, buf[0])
		pos++
		buf = buf[1:]
	}
	return out
}
