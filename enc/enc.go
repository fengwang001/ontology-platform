// Package enc is the streaming LZ77 compressor plus deterministic parallel mode.
package enc

import (
	"bytes"
	"errors"
	"hash"
	"runtime"
	"sync"

	"ontology/match"
	"ontology/wire"
)

// Config configures a compressor.
type Config struct {
	WindowCap int // history window capacity
	Chain     int // max hash-chain candidates examined per position
}

// Defaults and validation.
var ErrConfig = errors.New("enc: window and chain must be > 0")

func (c Config) withDefaults() (Config, error) {
	if c.WindowCap == 0 {
		c.WindowCap = 1 << 15
	}
	if c.Chain == 0 {
		c.Chain = 32
	}
	if c.WindowCap <= 0 || c.Chain <= 0 {
		return c, ErrConfig
	}
	return c, nil
}

// Writer is a single-stream compressor; not safe for concurrent use.
type Writer struct {
	cfg    Config
	out    *bytes.Buffer
	mt     *match.Matcher
	pend   []byte
	lit    []byte
	hash   hash.Hash64
	total  uint64
	dirty  bool
	hdr    bool
	closed bool
}

// NewWriter creates a streaming compressor writing into out.
func NewWriter(out *bytes.Buffer, cfg Config) (*Writer, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	mt, err := match.New(c.WindowCap, c.Chain)
	if err != nil {
		return nil, err
	}
	return &Writer{cfg: c, out: out, mt: mt, hash: wire.NewHash()}, nil
}

// Matcher exposes the matcher (for complexity accounting).
func (w *Writer) Matcher() *match.Matcher { return w.mt }

func (w *Writer) ensureHeader() {
	if !w.hdr {
		w.out.WriteString(wire.Header)
		w.hdr = true
	}
}

func (w *Writer) emitLit() {
	if len(w.lit) == 0 {
		return
	}
	w.out.WriteByte(wire.TagLit)
	var b [10]byte
	w.out.Write(wire.PutUvarint(b[:0], uint64(len(w.lit))))
	w.out.Write(w.lit)
	w.lit = w.lit[:0]
}

func (w *Writer) emitMatch(dist, length int) {
	w.emitLit()
	w.out.WriteByte(wire.TagMatch)
	var b [32]byte
	p := wire.PutUvarint(b[:0], uint64(dist))
	p = wire.PutUvarint(p, uint64(length))
	w.out.Write(p)
}

// Write feeds input; compressed bytes may be buffered across Write boundaries.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("enc: write after close")
	}
	if len(p) == 0 {
		return 0, nil
	}
	w.ensureHeader()
	w.pend = append(w.pend, p...)
	w.hash.Write(p)
	w.total += uint64(len(p))
	w.dirty = true
	w.process(false)
	return len(p), nil
}

// Flush settles every pending byte and marks a recoverable boundary.
// A second Flush with no intervening input emits no bytes.
func (w *Writer) Flush() error {
	if w.closed {
		return errors.New("enc: flush after close")
	}
	w.ensureHeader()
	if !w.dirty {
		return nil
	}
	w.process(true)
	w.emitLit()
	w.out.WriteByte(wire.TagFlush)
	w.dirty = false
	return nil
}

// Close writes the trailer; no more input is accepted afterwards.
func (w *Writer) Close() error {
	if w.closed {
		return errors.New("enc: double close")
	}
	w.ensureHeader()
	w.process(true)
	w.emitLit()
	w.out.WriteByte(wire.TagEnd)
	var b [32]byte
	p := wire.PutUvarint(b[:0], w.total)
	p = wire.PutUvarint(p, w.hash.Sum64())
	w.out.Write(p)
	w.closed = true
	w.dirty = false
	return nil
}

type blockJob struct {
	data []byte
	dict []byte
	idx  int
}

func compressBlock(cfg Config, data, dict []byte) ([]byte, error) {
	mt, err := match.New(cfg.WindowCap, cfg.Chain)
	if err != nil {
		return nil, err
	}
	mt.Insert(dict)
	var buf bytes.Buffer
	pend := append([]byte(nil), data...)
	var lit []byte
	i := 0
	for i < len(pend) {
		d, l := mt.Match(pend[i:], len(pend)-i)
		if l >= match.MinMatch {
			if len(lit) > 0 {
				buf.WriteByte(wire.TagLit)
				var b [10]byte
				buf.Write(wire.PutUvarint(b[:0], uint64(len(lit))))
				buf.Write(lit)
				lit = lit[:0]
			}
			buf.WriteByte(wire.TagMatch)
			var b [32]byte
			p := wire.PutUvarint(b[:0], uint64(d))
			p = wire.PutUvarint(p, uint64(l))
			buf.Write(p)
			mt.Insert(pend[i : i+l])
			i += l
			continue
		}
		lit = append(lit, pend[i])
		mt.Insert(pend[i : i+1])
		i++
	}
	if len(lit) > 0 {
		buf.WriteByte(wire.TagLit)
		var b [10]byte
		buf.Write(wire.PutUvarint(b[:0], uint64(len(lit))))
		buf.Write(lit)
	}
	return buf.Bytes(), nil
}

// CompressParallel splits data into fixed blocks compressed independently by
// workers; block i may reference up to WindowCap bytes from block i-1 as a
// preset dictionary. Output is identical for any positive worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be > 0")
	}
	nb := (len(data) + blockSize - 1) / blockSize
	jobs := make([]blockJob, nb)
	for i := range jobs {
		s, e := i*blockSize, (i+1)*blockSize
		if e > len(data) {
			e = len(data)
		}
		d0 := s - c.WindowCap
		if d0 < 0 {
			d0 = 0
		}
		jobs[i] = blockJob{data: data[s:e], dict: data[d0:s], idx: i}
	}
	if workers > nb && nb > 0 {
		workers = nb
	}
	if workers > runtime.NumCPU()*2 {
		workers = runtime.NumCPU() * 2
	}
	results := make([][]byte, nb)
	errs := make([]error, nb)
	ch := make(chan blockJob)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				results[j.idx], errs[j.idx] = compressBlock(c, j.data, j.dict)
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	var out bytes.Buffer
	out.WriteString(wire.Header)
	for _, r := range results {
		out.Write(r)
	}
	out.WriteByte(wire.TagEnd)
	h := wire.NewHash()
	h.Write(data)
	var b [32]byte
	p := wire.PutUvarint(b[:0], uint64(len(data)))
	p = wire.PutUvarint(p, h.Sum64())
	out.Write(p)
	return out.Bytes(), nil
}

// process commits all but the last pending byte into decided records.
func (w *Writer) process(final bool) {
	i := 0
	usable := len(w.pend)
	if !final {
		usable-- // the very last byte can still extend a match ending here
	}
	for i < usable {
		d, l := w.mt.Match(w.pend[i:], usable-i)
		if l > 0 && i+l <= usable {
			w.emitMatch(d, l)
			w.mt.Insert(w.pend[i : i+l])
			i += l
			continue
		}
		w.lit = append(w.lit, w.pend[i])
		w.mt.Insert(w.pend[i : i+1])
		i++
	}
	if i > 0 {
		w.pend = append(w.pend[:0], w.pend[i:]...)
	}
}
