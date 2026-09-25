// Package enc implements the streaming LZ77 compressor.
package enc

import (
	"errors"
	"hash/crc32"
	"sync"

	"ontology/match"
	"ontology/wire"
)

// Default tuning constants.
const (
	DefaultWindow = 1 << 16
	DefaultChain  = 64
)

// Config configures a streaming compressor.
type Config struct {
	WindowCap int
	MaxChain  int
}

// Encoder compresses Write calls into one stream. A single Encoder is not
// safe for concurrent use.
type Encoder struct {
	out     []byte
	m       *match.Matcher
	pending []byte
	lit     []byte
	size    uint64
	crc     uint32
	hdr     bool
	closed  bool
}

// New returns an encoder; zero fields use defaults.
func New(c Config) (*Encoder, error) {
	wc, ch := c.WindowCap, c.MaxChain
	if wc == 0 {
		wc = DefaultWindow
	}
	if ch == 0 {
		ch = DefaultChain
	}
	m, err := match.New(wc, ch, match.MaxMatchLen)
	if err != nil {
		return nil, err
	}
	return &Encoder{m: m}, nil
}

func (e *Encoder) emitLit() {
	if len(e.lit) == 0 {
		return
	}
	e.out = wire.AppendLiteral(e.out, e.lit)
	e.lit = e.lit[:0]
}

// Examined returns the matcher's cumulative inspected-candidate count.
func (e *Encoder) Examined() int64 { return e.m.Examined() }

func (e *Encoder) flushLits() {
	e.emitLit()
}

// Write feeds plaintext. Compression of any byte is deferred until enough
// following bytes are known, so the result is independent of Write chunking.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("enc: write after close")
	}
	if !e.hdr {
		e.out = append(e.out, wire.Header[:]...)
		e.hdr = true
		e.crc = crc32.ChecksumIEEE(nil)
	}
	e.crc = crc32.Update(e.crc, crc32.IEEETable, p)
	e.size += uint64(len(p))
	e.pending = append(e.pending, p...)
	e.advance(false)
	return len(p), nil
}

// advance commits matches/literals. When all is false a holdback tail of
// match.MaxMatchLen bytes stays undecided.
func (e *Encoder) advance(all bool) {
	for len(e.pending) > match.MaxMatchLen || all && len(e.pending) > 0 {
		d, l := e.m.Find(e.pending)
		if l >= match.MinMatch && (all || l < len(e.pending)) {
			e.emitLit()
			e.out = wire.AppendMatch(e.out, d, l)
			e.m.Add(e.pending[:l])
			e.pending = e.pending[l:]
			continue
		}
		e.lit = append(e.lit, e.pending[0])
		e.m.Add(e.pending[:1])
		e.pending = e.pending[1:]
		if len(e.lit) >= 4096 {
			e.emitLit()
		}
	}
}

// Flush forces all input written so far to become decodable and returns the
// compressed bytes accumulated since the previous Flush/Write result.
func (e *Encoder) Flush() []byte {
	if !e.hdr {
		e.out = append(e.out, wire.Header[:]...)
		e.hdr = true
		e.crc = crc32.ChecksumIEEE(nil)
	}
	had := len(e.pending) > 0
	e.advance(true)
	e.flushLits()
	if had {
		e.out = append(e.out, wire.TagFlush)
	}
	r := e.out
	e.out = nil
	return r
}

// Close finalizes the stream with its trailer and returns all remaining bytes.
func (e *Encoder) Close() ([]byte, error) {
	if e.closed {
		return nil, errors.New("enc: already closed")
	}
	e.closed = true
	if !e.hdr {
		e.out = append(e.out, wire.Header[:]...)
		e.crc = crc32.ChecksumIEEE(nil)
	}
	e.advance(true)
	e.emitLit()
	e.out = wire.AppendEnd(e.out, e.size, uint64(e.crc))
	r := e.out
	e.out = nil
	return r, nil
}

// Compress returns the full compressed stream of data.
func Compress(data []byte, c Config) ([]byte, error) {
	e, err := New(c)
	if err != nil {
		return nil, err
	}
	if _, err := e.Write(data); err != nil {
		return nil, err
	}
	return e.Close()
}

type blockJob struct {
	data []byte
	prev []byte
	wc   int
	ch   int
}

func compressBlock(j blockJob) ([]byte, error) {
	m, err := match.New(j.wc, j.ch, match.MaxMatchLen)
	if err != nil {
		return nil, err
	}
	if len(j.prev) > 0 {
		m.Preset(j.prev)
	}
	e := &Encoder{m: m, hdr: true, crc: crc32.ChecksumIEEE(nil)}
	if _, err := e.Write(j.data); err != nil {
		return nil, err
	}
	e.advance(true)
	e.emitLit()
	e.out = append(e.out, wire.TagFlush)
	return e.out, nil
}

// CompressParallel splits data into fixed blocks compressed by worker
// goroutines. Each block may reference up to WindowCap bytes of the previous
// block's plaintext. Output is identical for any positive worker count and
// decodable by the streaming decoder.
func CompressParallel(data []byte, blockSize int, workers int, c Config) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errors.New("enc: blockSize must be > 0")
	}
	if workers <= 0 {
		return nil, errors.New("enc: workers must be > 0")
	}
	wc, ch := c.WindowCap, c.MaxChain
	if wc == 0 {
		wc = DefaultWindow
	}
	if ch == 0 {
		ch = DefaultChain
	}
	n := (len(data) + blockSize - 1) / blockSize
	jobs := make([]blockJob, n)
	for i := range jobs {
		s, t := i*blockSize, min((i+1)*blockSize, len(data))
		ps := max(0, s-wc)
		jobs[i] = blockJob{data[s:t], data[ps:s], wc, ch}
	}
	parts := make([][]byte, n)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for i := range jobs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			b, err := compressBlock(jobs[i])
			<-sem
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			parts[i] = b
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	out := append([]byte{}, wire.Header[:]...)
	for _, b := range parts {
		out = append(out, b...)
	}
	size, crc := uint64(len(data)), crc32.ChecksumIEEE(data)
	out = wire.AppendEnd(out, size, uint64(crc))
	return out, nil
}
