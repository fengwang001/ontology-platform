// Package enc is a streaming LZ77 compressor for the wire format.
//
// A single Writer is not safe for concurrent use.
package enc

import (
	"hash/crc32"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	DefaultWindow  = 1 << 16
	DefaultChain   = 64
	maxMatchLength = 1 << 30
)

// Writer compresses bytes into an underlying io.Writer.
type Writer struct {
	out      io.Writer
	m        *match.Matcher
	pending  []byte
	cursor   int
	started  bool
	closed   bool
	flushed  bool
	winCap   int
	total    int
	crc      uint32
	litStart int
}

// NewWriter validates the configuration and builds a compressor.
func NewWriter(out io.Writer, windowCap, maxChain int) (*Writer, error) {
	if windowCap <= 0 || maxChain <= 0 {
		return nil, wire.ErrConfig
	}
	return &Writer{
		out: out, m: match.New(window.New(windowCap), maxChain), winCap: windowCap,
		litStart: -1,
	}, nil
}

// process finalizes every token except one possibly truncated by buf's end,
// unless final is set (Flush/Close declare no later bytes).
func (w *Writer) process(final bool) error {
	for w.cursor < len(w.pending) {
		remain := len(w.pending) - w.cursor
		d, l := w.m.Find(w.cursor, remain)
		hold := !final && (l == remain && remain >= match.MinMatch || remain < match.MinMatch)
		if hold {
			break
		}
		if w.litStart >= 0 {
			if w.cursor > w.litStart {
				seg := w.pending[w.litStart:w.cursor]
				if _, err := w.out.Write(wire.AppendLiteral(nil, seg)); err != nil {
					return err
				}
			}
			w.litStart = -1
		}
		if l >= match.MinMatch {
			if _, err := w.out.Write(wire.AppendBackref(nil, d, l)); err != nil {
				return err
			}
			w.cursor += l
			continue
		}
		if w.litStart < 0 {
			w.litStart = w.cursor
		}
		w.cursor++
	}
	if final && w.litStart >= 0 {
		if seg := w.pending[w.litStart:w.cursor]; len(seg) > 0 {
			if _, err := w.out.Write(wire.AppendLiteral(nil, seg)); err != nil {
				return err
			}
		}
		w.litStart = -1
	}
	return nil
}

// Write appends input; compressed output may lag across Write boundaries.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, wire.ErrTrailing
	}
	if !w.started {
		if _, err := w.out.Write(wire.AppendHeader(nil, w.winCap)); err != nil {
			return 0, err
		}
		w.started = true
	}
	keep := w.pending[w.cursor:]
	oldCursor := w.cursor
	np := make([]byte, 0, len(keep)+len(p))
	np = append(np, keep...)
	np = append(np, p...)
	w.pending = np
	w.cursor = 0
	if w.litStart >= 0 {
		w.litStart -= oldCursor
	}
	w.total += len(p)
	w.crc = crc32.Update(w.crc, crc32.IEEETable, p)
	w.m.Ingest(p)
	w.flushed = false
	if err := w.process(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush makes all input so far recoverable by a decoder; a second Flush
// without new input emits nothing.
func (w *Writer) Flush() error {
	if w.closed {
		return wire.ErrTrailing
	}
	if !w.started {
		if _, err := w.out.Write(wire.AppendHeader(nil, w.winCap)); err != nil {
			return err
		}
		w.started = true
	}
	if w.flushed {
		return nil
	}
	if err := w.process(true); err != nil {
		return err
	}
	w.cursor = len(w.pending)
	w.flushed = true
	_, err := w.out.Write(wire.AppendFlush(nil))
	return err
}

// Close writes the stream end record; it must be called exactly once.
func (w *Writer) Close() error {
	if w.closed {
		return wire.ErrTrailing
	}
	if !w.started {
		if _, err := w.out.Write(wire.AppendHeader(nil, w.winCap)); err != nil {
			return err
		}
		w.started = true
	}
	if err := w.process(true); err != nil {
		return err
	}
	w.closed = true
	_, err := w.out.Write(wire.AppendEnd(nil, w.total, w.crc))
	return err
}

func compressBlock(prev, block []byte, winCap, maxChain int) []byte {
	m := match.New(window.New(winCap), maxChain)
	if len(prev) > 0 {
		m.Preset(prev)
	}
	bw := &sliceWriter{}
	cw := &Writer{out: bw, m: m, winCap: winCap, litStart: -1, started: true}
	for len(block) > 0 {
		n := maxMatchLength
		if n > len(block) {
			n = len(block)
		}
		if _, err := cw.Write(block[:n]); err != nil {
			panic(err)
		}
		block = block[n:]
	}
	if err := cw.process(true); err != nil {
		panic(err)
	}
	return bw.b
}

type sliceWriter struct{ b []byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

// CompressParallel compresses data in fixed blocks using worker goroutines.
// Every block may reference up to winCap trailing bytes of the previous one.
func CompressParallel(data []byte, blockSize, winCap, maxChain, workers int) ([]byte, error) {
	if blockSize <= 0 || winCap <= 0 || maxChain <= 0 || workers <= 0 {
		return nil, wire.ErrConfig
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if len(data) == 0 {
		nb = 1
	}
	chunks := make([][]byte, nb)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := 0; i < nb; i++ {
		lo, hi := i*blockSize, (i+1)*blockSize
		if hi > len(data) {
			hi = len(data)
		}
		var prev []byte
		if i > 0 {
			plo := lo - winCap
			if plo < 0 {
				plo = 0
			}
			prev = data[plo:lo]
		}
		block := data[lo:hi]
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, prev, block []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			chunks[i] = compressBlock(prev, block, winCap, maxChain)
		}(i, prev, block)
	}
	wg.Wait()
	out := wire.AppendHeader(nil, winCap)
	crc := crc32.ChecksumIEEE(data)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return wire.AppendEnd(out, len(data), crc), nil
}
