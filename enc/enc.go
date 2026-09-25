// Package enc implements streaming and parallel LZ77 compression.
// A Writer is not safe for concurrent use.
package enc

import (
	"errors"
	"sync"

	"ontology/match"
	"ontology/wire"
)

const DefaultWindow, DefaultChain int = 1 << 15, 32

var ErrBadConfig = errors.New("enc: window size, max chain and block size must be positive")

// Writer output depends only on cumulative input, not Write boundaries.
type Writer struct {
	mt                *match.Matcher
	out, pend, lits   []byte
	total, sum        uint64
	winS, chain       int
	dirty, hdr, count bool
}

func New(windowSize, maxChain int) (w *Writer, err error) {
	w = &Writer{winS: windowSize, chain: maxChain, sum: wire.FNVInitial(), count: true}
	w.mt, err = match.New(windowSize, maxChain)
	return w, err
}

func (w *Writer) Write(p []byte) (int, error) {
	if !w.hdr {
		w.out = wire.AppendHeader(w.out, uint64(w.winS), uint64(w.chain))
		w.hdr = true
	}
	if w.count {
		w.total += uint64(len(p))
		w.sum = wire.FNV(w.sum, p)
	}
	w.pend = append(w.pend, p...)
	w.mt.Append(p)
	w.advance(false)
	return len(p), nil
}

// advance commits safe positions, keeping MinMatch-1 tail bytes; DESIGN 1.
func (w *Writer) advance(flush bool) {
	for n := len(w.pend); n >= wire.MinMatch || (flush && n > 0); n = len(w.pend) {
		pos := int(w.total) - n
		d, l := 0, 0
		if n >= wire.MinMatch {
			d, l = w.mt.Find(pos)
		}
		if l < wire.MinMatch || (!flush && l > n-wire.MinMatch+1) {
			w.lits = append(w.lits, w.pend[0])
			w.pend = w.pend[1:]
			continue
		}
		w.emitLits()
		w.out = wire.AppendTag(w.out, wire.TagMatch, uint64(d-1))
		w.out = wire.AppendUvarint(w.out, uint64(l-1))
		w.pend = w.pend[l:]
		w.dirty = true
	}
}

func (w *Writer) emitLits() {
	if n := len(w.lits); n > 0 {
		w.out = wire.AppendTag(w.out, wire.TagLit, uint64(n-1))
		w.out = append(w.out, w.lits...)
		w.lits = w.lits[:0]
		w.dirty = true
	}
}

func (w *Writer) flushMark() {
	if w.dirty {
		w.out = wire.AppendTag(w.out, wire.TagFlush, 0)
		w.dirty = false
	}
}

// Flush represents all accepted input; an empty repeat Flush emits nothing.
func (w *Writer) Flush() error {
	w.advance(true)
	w.emitLits()
	w.flushMark()
	return nil
}

func (w *Writer) finish() []byte {
	w.advance(true)
	w.emitLits()
	w.flushMark()
	w.out = wire.AppendTag(w.out, wire.TagEnd, w.total)
	return wire.AppendUvarint(w.out, w.sum)
}

func (w *Writer) Close() ([]byte, error) { return append([]byte(nil), w.finish()...), nil }

// CompressParallel splits data into fixed blocks; output is worker-count
// independent (each block seeds from the previous block's tail), DESIGN 4.
func CompressParallel(data []byte, blockSize, workers int) (out []byte, err error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrBadConfig
	}
	n := (len(data) + blockSize - 1) / blockSize
	if len(data) == 0 {
		n = 1
	}
	parts, jobs := make([][]byte, n), make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range jobs {
				parts[k] = compressBlock(data, k, blockSize)
			}
		}()
	}
	for k := 0; k < n; k++ {
		jobs <- k
	}
	close(jobs)
	wg.Wait()
	out = wire.AppendHeader(nil, uint64(DefaultWindow), uint64(DefaultChain))
	for _, p := range parts {
		out = append(out, p...)
	}
	return out, nil
}

func compressBlock(data []byte, k, bs int) []byte {
	start := k * bs
	end := min(len(data), start+bs)
	dStart := max(0, start-DefaultWindow)
	w, _ := New(DefaultWindow, DefaultChain)
	w.hdr, w.count = true, false
	if start > 0 {
		w.mt.Append(data[dStart:start])
		w.out = wire.AppendTag(w.out, wire.TagDict, uint64(start-dStart))
	}
	w.count = true
	w.Write(data[start:end])
	return w.finish()
}
