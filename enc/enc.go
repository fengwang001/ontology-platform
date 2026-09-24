package enc

import (
	"errors"
	"hash/crc32"
	"ontology/match"
	"ontology/window"
	"ontology/wire"
	"sync"
)

const (
	DefaultWindowCap = 1 << 15
	DefaultChainMax  = 64
	maxMatch         = 1 << 20
)

var ErrInvalidConfig = errors.New("enc: invalid configuration")

type Writer struct {
	out, lit                     []byte
	win                          *window.Window
	mch                          *match.Matcher
	decided, have, omDist, omLen int
	closed, dirty, flushed       bool
	checksum                     uint32
	total                        int
}

func NewWriter(windowCap, chainMax int) (*Writer, error) {
	if windowCap <= 0 || chainMax <= 0 {
		return nil, ErrInvalidConfig
	}
	win := window.New(windowCap)
	return &Writer{out: wire.AppendHeader(nil), win: win, mch: match.New(win, chainMax)}, nil
}
func (w *Writer) Probes() int { return w.mch.Probes() }
func (w *Writer) emitLit()    { w.out = wire.AppendLiteral(w.out, w.lit); w.lit = w.lit[:0] }
func (w *Writer) add(pos int) {
	if pos >= 0 && pos+match.MinLength <= w.have { w.mch.Insert(pos) }
}
func (w *Writer) process(final bool) {
	limit := w.have
	if !final {
		limit -= match.MinLength - 1
	}
	for w.decided < limit {
		p := w.decided
		if w.omLen > 0 && p < w.have && w.win.ByteAt(p-w.omDist) == w.win.ByteAt(p) {
			w.omLen++
			w.add(p - match.MinLength + 1)
			w.decided++
			continue
		}
		if w.omLen > 0 {
			w.out = wire.AppendMatch(w.out, w.omDist, w.omLen)
			w.omDist, w.omLen = 0, 0
		}
		avail := w.have - p
		if avail >= match.MinLength {
			if l, d, _ := w.mch.Find(p, min(maxMatch, w.win.Cap(), avail)); l >= match.MinLength {
				w.emitLit()
				w.omDist, w.omLen = d, l
				for i := 0; i < l; i++ {
					w.add(p + i - match.MinLength + 1)
				}
				w.decided = p + l
				continue
			}
		}
		w.lit = append(w.lit, w.win.ByteAt(p))
		w.add(p)
		w.decided++
	}
}
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed { return 0, errors.New("enc: write after close") }
	w.win.Push(p)
	w.have += len(p)
	w.checksum = crc32.Update(w.checksum, crc32.IEEETable, p)
	w.total += len(p)
	w.dirty = w.dirty || len(p) > 0
	w.process(false)
	return len(p), nil
}
func (w *Writer) force() {
	w.process(true)
	if w.omLen > 0 {
		w.out = wire.AppendMatch(w.out, w.omDist, w.omLen)
		w.omLen = 0
	}
	w.emitLit()
}
func (w *Writer) Flush() error {
	if w.closed { return errors.New("enc: flush after close") }
	if !w.flushed || w.dirty {
		w.force()
		w.out = wire.AppendFlush(w.out)
		w.flushed, w.dirty = true, false
	}
	return nil
}
func (w *Writer) Close() ([]byte, error) {
	if w.closed { return nil, errors.New("enc: close twice") }
	w.force()
	w.out = wire.AppendEnd(w.out, uint64(w.total), uint64(w.checksum))
	w.closed = true
	return w.out, nil
}
func CompressParallel(data []byte, blockSize, workers, windowCap, chainMax int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 || windowCap <= 0 || chainMax <= 0 {
		return nil, ErrInvalidConfig
	}
	num := (len(data) + blockSize - 1) / blockSize
	blocks := make([][]byte, num)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				blocks[i] = compressBlock(data, i, blockSize, windowCap, chainMax)
			}
		}()
	}
	for i := 0; i < num; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	out := wire.AppendHeader(nil)
	for _, b := range blocks {
		out = append(out, b...)
	}
	return wire.AppendEnd(out, uint64(len(data)), uint64(crc32.ChecksumIEEE(data))), nil
}
func compressBlock(data []byte, idx, blockSize, windowCap, chainMax int) []byte {
	start := idx * blockSize
	end := min(start+blockSize, len(data))
	w, _ := NewWriter(windowCap, chainMax)
	dStart := max(0, start-windowCap)
	w.win.Push(data[dStart:start])
	w.have, w.decided = start-dStart, start-dStart
	w.Write(data[start:end])
	w.force()
	return w.out[len(wire.AppendHeader(nil)):]
}
