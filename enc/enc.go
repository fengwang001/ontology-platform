// Package enc is the streaming LZ77 compressor and the parallel block compressor.
package enc

import (
	"errors"
	"hash/fnv"
	"sync"

	"ontology/match"
	"ontology/wire"
)

type Config struct {
	WindowCap int
	ChainLen  int
}

const (
	DefaultWindowCap = 1 << 15
	DefaultChainLen  = 64
)

var (
	ErrBadConfig = errors.New("enc: window capacity and chain length must be positive")
	ErrClosed    = errors.New("enc: writer already closed")
)

// Writer compresses bytes into one LZ77 stream; not safe for concurrent use.
type Writer struct {
	out        []byte
	mat        *match.Matcher
	pend       []byte
	lit        []byte
	started    bool
	closed     bool
	flushedEnd int64
	totalLen   int64
	sum        fnv.Hash64
}

func NewWriter(cfg Config) (*Writer, error) {
	wc, cl := cfg.WindowCap, cfg.ChainLen
	if wc == 0 {
		wc = DefaultWindowCap
	}
	if cl == 0 {
		cl = DefaultChainLen
	}
	mat, err := match.New(wc, cl)
	if err != nil {
		return nil, ErrBadConfig
	}
	return &Writer{mat: mat, sum: fnv.New64a()}, nil
}

func (w *Writer) ensureStart() {
	if !w.started {
		w.out = wire.AppendHeader(w.out)
		w.started = true
	}
}

func (w *Writer) flushLits() {
	if len(w.lit) > 0 {
		w.out = wire.AppendLiteral(w.out, w.lit)
		w.lit = w.lit[:0]
	}
}

// commit moves the first n pending bytes into the window, indexes their triples,
// and folds them into the checksum.
func (w *Writer) commit(n int) {
	win := w.mat.Win()
	w.sum.Write(w.pend[:n])
	for i := 0; i < n; i++ {
		win.Add(w.pend[i])
	}
	last := win.End() - 1
	for p := max(int64(0), win.End()-int64(n)-2); p <= last; p++ {
		if p < 0 {
			continue
		}
		if p+2 <= last {
			w.mat.IndexPosition(p)
		}
	}
	w.pend = w.pend[n:]
	w.totalLen += int64(n)
}

// settle finalizes positions whose result cannot change with more input.
// In streaming mode the final pending byte is retained; force settles all.
func (w *Writer) settle(force bool) {
	keep := 1
	if force {
		keep = 0
	}
	for len(w.pend) > keep {
		d, l := w.mat.Find(w.mat.Win().End(), w.pend)
		if l >= match.MinMatch {
			w.flushLits()
			w.out = wire.AppendMatch(w.out, uint64(d), uint64(l))
			w.commit(l)
			// A match may consume the retained trailing byte; re-evaluate keep.
			continue
		}
		w.lit = append(w.lit, w.pend[0])
		w.commit(1)
}
}

func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	w.pend = append(w.pend, p...)
	w.settle(false)
	return len(p), nil
}

func (w *Writer) Flush() error {
	if w.closed {
		return ErrClosed
	}
	if w.totalLen == w.flushedEnd && len(w.pend) == 0 {
		return nil
	}
	w.ensureStart()
	w.settle(true)
	w.flushLits()
	w.out = wire.AppendFlush(w.out)
	w.flushedEnd = w.totalLen
	return nil
}

func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	w.ensureStart()
	w.settle(true)
	w.flushLits()
	w.out = wire.AppendEnd(w.out, uint64(w.totalLen), w.sum.Sum64())
	w.closed = true
	return nil
}

func (w *Writer) Bytes() []byte { return w.out }

// putDict seeds the compressor with dictionary bytes that are not emitted.
func (w *Writer) putDict(dict []byte) {
	win := w.mat.Win()
	for i := 0; i < len(dict); i++ {
		win.Add(dict[i])
	}
	last := win.End() - 1
	for p := int64(0); p <= last; p++ {
		if p+2 <= last {
			w.mat.IndexPosition(p)
		}
	}
}

// compressBlock compresses one block with an optional prefix dictionary.
func compressBlock(dict, block []byte) []byte {
	w, _ := NewWriter(Config{WindowCap: DefaultWindowCap, ChainLen: DefaultChainLen})
	if len(dict) > 0 {
		w.putDict(dict)
	}
	w.ensureStart()
	_, _ = w.Write(block)
	w.settle(true)
	w.flushLits()
	// Strip the header emitted by ensureStart; records are concatenated.
	return w.out[len(wire.AppendHeader(nil)):]
}

// CompressParallel compresses data as fixed-size blocks with up to workers
// goroutines. Each block may back-reference the final window-sized bytes of the
// preceding block. The output is one ordinary stream and is byte-identical for
// any workers value. workers<=0 is rejected.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrBadConfig
	}
	n := (len(data) + blockSize - 1) / blockSize
	blocks := make([][]byte, n)
	type job struct{ i int }
	jobs := make(chan job)
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				lo := j.i * blockSize
				hi := min(len(data), lo+blockSize)
				dlo := max(0, lo-DefaultWindowCap)
				var blk []byte
				if lo < len(data) {
					blk = compressBlock(data[dlo:lo], data[lo:hi])
				}
				blocks[j.i] = blk
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- job{i}
	}
	close(jobs)
	wg.Wait()
	out := wire.AppendHeader(nil)
	for _, b := range blocks {
		out = append(out, b...)
	}
	sum := fnv.New64a()
	sum.Write(data)
	out = wire.AppendEnd(out, uint64(len(data)), sum.Sum64())
	return out, nil
}
