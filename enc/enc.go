// Package enc is a streaming LZ77 compressor with deterministic parallel
// block compression. A single Compressor is not safe for concurrent use.
package enc

import (
	"errors"
	"sync"

	"ontology/match"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 15
	DefaultChain  = 32
	fnvOffset     = 14695981039346656037
	fnvPrime      = 1099511628211
)

// Config configures a Compressor.
type Config struct {
	Window   int
	MaxChain int
}

// Compressor buffers undecided input so output never depends on Write cuts.
type Compressor struct {
	out    []byte
	mt     *match.Matcher
	pend   []byte
	total  int
	sum    uint64
	dirty  bool
	closed bool
}

// New validates configuration and writes the stream header.
func New(c Config) (*Compressor, error) {
	w, ch := c.Window, c.MaxChain
	if w == 0 {
		w = DefaultWindow
	}
	if ch == 0 {
		ch = DefaultChain
	}
	mt, err := match.New(w, ch)
	if err != nil {
		return nil, err
	}
	return &Compressor{out: wire.AppendHeader(nil, w), mt: mt, sum: fnvOffset}, nil
}

// Write appends input and drains every prefix whose outcome is already fixed:
// a match is final only once a mismatch/lookahead byte is buffered.
func (c *Compressor) Write(p []byte) (int, error) {
	c.pend = append(c.pend, p...)
	c.dirty = true
	for _, b := range p {
		c.sum ^= uint64(b)
		c.sum *= fnvPrime
	}
	c.total += len(p)
	for len(c.pend) > match.MinMatch {
		c.drain(false)
	}
	return len(p), nil
}

// force=true allows the match to consume all buffered bytes (Flush/Close).
func (c *Compressor) drain(force bool) {
	c.mt.SetPending(c.pend)
	avail := len(c.pend)
	if !force {
		avail--
	}
	d, l := c.mt.Find(0, avail)
	if l == 0 {
		c.out = wire.AppendLiteral(c.out, c.pend[:1])
		l = 1
	} else {
		c.out = wire.AppendMatch(c.out, d, l)
	}
	c.mt.Advance(l)
	c.pend = c.mt.Pending()
}

// Flush forces the current match to end and writes a synchronization marker.
// A second Flush with no new input emits nothing.
func (c *Compressor) Flush() error {
	if !c.dirty {
		return nil
	}
	for len(c.pend) > 0 {
		c.drain(true)
	}
	if c.dirty {
		c.out = append(c.out, wire.TagFlush)
	}
	c.dirty = false
	return nil
}

// Close emits the end record with total length and FNV-1a checksum.
func (c *Compressor) Close() error {
	if c.closed {
		return errors.New("enc: already closed")
	}
	for len(c.pend) > 0 {
		c.drain(true)
	}
	c.out = append(c.out, wire.TagEnd)
	c.out = wire.AppendUvarint(c.out, uint64(c.total))
	c.out = wire.AppendUvarint(c.out, c.sum)
	c.closed = true
	return nil
}

// Bytes returns compressed bytes produced so far.
func (c *Compressor) Bytes() []byte { return c.out }

// Compress is a one-shot helper honoring the same format.
func Compress(data []byte) ([]byte, error) {
	c, _ := New(Config{})
	if _, err := c.Write(data); err != nil {
		return nil, err
	}
	if err := c.Close(); err != nil {
		return nil, err
	}
	return append([]byte(nil), c.out...), nil
}

// CompressParallel splits data into fixed blocks compressed concurrently. Each
// block may reference up to Window bytes of the previous block's tail; the
// stream is byte-identical for any workers value.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errors.New("enc: blockSize must be > 0")
	}
	if workers <= 0 {
		return nil, errors.New("enc: workers must be > 0")
	}
	nb := (len(data) + blockSize - 1) / blockSize
	toks := make([][]byte, nb)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				lo, hi := j*blockSize, (j+1)*blockSize
				if hi > len(data) {
					hi = len(data)
				}
				dict := data[max0(lo-DefaultWindow):lo]
				toks[j] = encodeBlock(dict, data[lo:hi])
			}
		}()
	}
	for j := 0; j < nb; j++ {
		jobs <- j
	}
	close(jobs)
	wg.Wait()
	out := wire.AppendHeader(nil, DefaultWindow)
	for _, t := range toks {
		out = append(out, t...)
	}
	out = append(out, wire.TagEnd)
	out = wire.AppendUvarint(out, uint64(len(data)))
	out = wire.AppendUvarint(out, checksum(data))
	return out, nil
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

// encodeBlock compresses one block with dict as a preset history. The greedy
// loop is the same deterministic rule used by the streaming compressor.
func encodeBlock(dict, block []byte) []byte {
	mt, _ := match.New(DefaultWindow, DefaultChain)
	mt.Preset(dict)
	var out []byte
	for k := 0; k < len(block); {
		mt.SetPending(block[k:])
		d, l := mt.Find(0, len(block)-k)
		if l == 0 {
			out = wire.AppendLiteral(out, block[k:k+1])
			l = 1
		} else {
			out = wire.AppendMatch(out, d, l)
		}
		mt.Advance(l)
		k += l
	}
	return out
}

func checksum(p []byte) uint64 {
	h := uint64(fnvOffset)
	for _, b := range p {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return h
}
