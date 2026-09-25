package enc

import (
	"errors"
	"hash/crc32"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 16
	DefaultChain  = 32
)

type Option func(*Compressor)
type Compressor struct {
	out, pend, lit     []byte
	m                  *match.Matcher
	sum                uint32
	total, capn, chain int
	dirty, closed      bool
}

func WithWindow(n int) Option { return func(c *Compressor) { c.capn = n } }
func WithChain(n int) Option  { return func(c *Compressor) { c.chain = n } }
func New(opts ...Option) (*Compressor, error) {
	c := &Compressor{capn: DefaultWindow, chain: DefaultChain}
	for _, opt := range opts {
		opt(c)
	}
	w, err := window.New(c.capn)
	if err != nil {
		return nil, err
	}
	if c.m, err = match.New(w, c.chain); err != nil {
		return nil, err
	}
	c.out = wire.Header()
	return c, nil
}
func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: closed")
	}
	c.pend, c.sum = append(c.pend, p...), crc32.Update(c.sum, crc32.IEEETable, p)
	c.total, c.dirty = c.total+len(p), true
	c.process(false)
	return len(p), nil
}
func (c *Compressor) litOut() {
	if len(c.lit) > 0 {
		c.out, c.lit = wire.AppendLiteral(c.out, c.lit), c.lit[:0]
	}
}
func emit(m *match.Matcher, out, lit *[]byte, p []byte, force bool) []byte {
	d, l := m.Find(p)
	if l >= 3 && (force || l < len(p)) {
		if len(*lit) > 0 {
			*out, *lit = wire.AppendLiteral(*out, *lit), (*lit)[:0]
		}
		*out = wire.AppendMatch(*out, uint64(d), uint64(l))
		for range l {
			m.Insert(p)
			p = p[1:]
		}
		return p
	}
	if force || l < 3 {
		*lit = append(*lit, p[0])
		m.Insert(p)
		return p[1:]
	}
	return p
}
func (c *Compressor) process(force bool) {
	for len(c.pend) >= 3 {
		before := len(c.pend)
		c.pend = emit(c.m, &c.out, &c.lit, c.pend, force)
		if !force && len(c.pend) == before {
			return
		}
	}
	if !force {
		return
	}
	c.litOut()
	for _, b := range c.pend {
		c.lit = append(c.lit, b)
		c.m.Insert([]byte{b})
	}
	c.pend = c.pend[:0]
	c.litOut()
}
func (c *Compressor) Flush() error {
	return c.finish(false)
}
func (c *Compressor) Close() error {
	return c.finish(true)
}
func (c *Compressor) finish(closeStream bool) error {
	if c.closed { return errors.New("enc: closed") }
	if !closeStream && !c.dirty { return nil }
	c.process(true)
	if closeStream {
		c.out = wire.AppendTail(c.out, uint64(c.total), uint64(c.sum))
		c.closed = true
	} else {
		c.out, c.dirty = wire.AppendFlush(c.out), false
	}
	return nil
}
func (c *Compressor) Output() []byte { return append([]byte(nil), c.out...) }

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: invalid configuration")
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	out := wire.Header()
	if workers == 1 {
		for i := range n {
			out = append(out, block(data, i, blockSize)...)
		}
	} else {
		blocks, jobs := make([][]byte, n), make(chan int)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					blocks[i] = block(data, i, blockSize)
				}
			}()
		}
		for i := range blocks {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		for _, b := range blocks {
			out = append(out, b...)
		}
	}
	return wire.AppendTail(out, uint64(len(data)), uint64(crc32.ChecksumIEEE(data))), nil
}
func block(data []byte, index, size int) []byte {
	start, end := index*size, min((index+1)*size, len(data))
	ds := max(0, start-DefaultWindow)
	dict, dataBlock := data[ds:start], data[start:end]
	w, _ := window.New(DefaultWindow)
	m, _ := match.New(w, DefaultChain)
	if len(dict) > 0 {
		m.Prepare(dict, dataBlock)
	}
	var out, lit []byte
	p := dataBlock
	for len(p) >= 3 {
		p = emit(m, &out, &lit, p, true)
	}
	for _, b := range p {
		lit = append(lit, b)
		m.Insert([]byte{b})
	}
	if len(lit) > 0 {
		out = wire.AppendLiteral(out, lit)
	}
	return out
}
