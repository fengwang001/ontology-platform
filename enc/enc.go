// Package enc 实现自定义 LZ77 流式压缩器与按块并行压缩。
package enc

import (
	"errors"
	"hash/crc32"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

var (
	ErrBadWindow     = errors.New("enc: window capacity must be >= 3")
	ErrBadChainLimit = errors.New("enc: chain limit must be >= 1")
	ErrBadBlockSize  = errors.New("enc: block size must be >= 1")
	ErrBadWorkers    = errors.New("enc: workers must be >= 1")
	ErrClosed        = errors.New("enc: writer already closed")
)

type core struct {
	cfg   wire.Config
	win   *window.Window
	mat   *match.Matcher
	out   []byte
	lits  []byte
	total uint64
	sum   uint32
}

func newCore(winCap, chainLimit int) *core {
	win := window.New(winCap)
	return &core{
		cfg: wire.Config{Window: winCap, ChainLimit: chainLimit},
		win: win,
		mat: match.New(win, chainLimit),
	}
}

func (c *core) flushLits() {
	if len(c.lits) == 0 {
		return
	}
	c.out = wire.AppendUvarint(c.out, wire.TagLiteral)
	c.out = wire.AppendUvarint(c.out, uint64(len(c.lits)))
	c.out = append(c.out, c.lits...)
	c.lits = c.lits[:0]
}

func (c *core) consume(data []byte) {
	c.win.PutBytes(data)
	c.total += uint64(len(data))
	c.sum = crc32.Update(c.sum, crc32.IEEETable, data)
}

// runBlock 在固定缓冲区 buf（含可选预置字典前缀 dictLen）上压缩最后一段，
// 返回压缩后的记录字节。该函数是流式与并行压缩共享的确定性核心。
func runBlock(winCap, chainLimit int, block, dict []byte) []byte {
	c := newCore(winCap, chainLimit)
	c.win.PutBytes(dict)
	off := uint64(len(dict))
	buf := append(append(make([]byte, 0, len(dict)+len(block)), dict...), block...)
	base := off
	c.total = uint64(len(block))
	first := off - uint64(c.win.Cap())
	if first < 0 {
		first = 0
	}
	for q := first; q+2 < off; q++ {
		c.mat.Insert(q)
	}
	p := off
	for p < uint64(len(buf)) {
		avail := len(buf) - int(p)
		d, l := c.mat.Find(p, avail)
		if avail > wire.MinMatchLen {
			if _, l2 := c.mat.Find(p+1, avail-1); l2 > l {
				c.lits = append(c.lits, buf[p])
				if p >= base {
					c.mat.Insert(p)
				}
				p++
				continue
			}
		}
		if l < wire.MinMatchLen {
			c.lits = append(c.lits, buf[p])
			c.mat.Insert(p)
			p++
			continue
		}
		c.flushLits()
		c.out = wire.AppendUvarint(c.out, wire.TagMatch)
		c.out = wire.AppendUvarint(c.out, uint64(d))
		c.out = wire.AppendUvarint(c.out, uint64(l))
		for i := 0; i < l; i++ {
			c.mat.Insert(p + uint64(i))
		}
		p += uint64(l)
	}
	c.flushLits()
	return c.out
}

// Writer 是单 goroutine 使用的流式压缩器（非并发安全）。
type Writer struct {
	winCap, chain int
	win           *window.Window
	mat           *match.Matcher
	out, pend     []byte
	lits          []byte
	start         uint64
	total         uint64
	sum           uint32
	closed, dirty bool
}

// NewWriter 创建压缩器并写入流头。
func NewWriter(winCap, chainLimit int) (*Writer, error) {
	if winCap < 3 {
		return nil, ErrBadWindow
	}
	if chainLimit < 1 {
		return nil, ErrBadChainLimit
	}
	w := &Writer{winCap: winCap, chain: chainLimit,
		win: window.New(winCap)}
	w.mat = match.New(w.win, chainLimit)
	w.out = wire.AppendHeader(nil, wire.Config{Window: winCap, ChainLimit: chainLimit})
	return w, nil
}

func (w *Writer) flushLits() {
	if len(w.lits) == 0 {
		return
	}
	w.out = wire.AppendUvarint(w.out, wire.TagLiteral)
	w.out = wire.AppendUvarint(w.out, uint64(len(w.lits)))
	w.out = append(w.out, w.lits...)
	w.lits = w.lits[:0]
}

// drain 只固化「再补字节也不会改变结果」的位置（见 DESIGN.md 第 1 节）。
func (w *Writer) drain(final bool) {
	for len(w.pend) >= wire.MinMatchLen {
		pos := w.start
		avail := len(w.pend)
		d1, l1 := w.mat.Find(pos, avail)
		r := l1
		if l1 < wire.MinMatchLen {
			r = 0
		}
		if !final && r+1 >= avail {
			return
		}
		if l1 < wire.MinMatchLen {
			w.lits = append(w.lits, w.pend[0])
			w.win.Put(w.pend[0])
			w.mat.Insert(pos)
			w.sum = crc32.Update(w.sum, crc32.IEEETable, w.pend[:1])
			w.total++
			w.pend, w.start = w.pend[1:], pos+1
			continue
		}
		d := d1
		if avail > wire.MinMatchLen {
			d2, l2 := w.mat.Find(pos+1, avail-1)
			if !final && l2+2 >= avail {
				return
			}
			if l2 > l1 {
				w.lits = append(w.lits, w.pend[0])
				w.win.Put(w.pend[0])
				w.mat.Insert(pos)
				w.sum = crc32.Update(w.sum, crc32.IEEETable, w.pend[:1])
				w.total++
				w.pend, w.start = w.pend[1:], pos+1
				continue
			}
			_ = d2
		}
		w.flushLits()
		w.out = wire.AppendUvarint(w.out, wire.TagMatch)
		w.out = wire.AppendUvarint(w.out, uint64(d))
		w.out = wire.AppendUvarint(w.out, uint64(l1))
		seg := w.pend[:l1]
		w.win.PutBytes(seg)
		w.sum = crc32.Update(w.sum, crc32.IEEETable, seg)
		w.total += uint64(l1)
		for i := 0; i < l1; i++ {
			w.mat.Insert(pos + uint64(i))
		}
		w.pend, w.start = w.pend[l1:], pos+uint64(l1)
}
	if final {
		for _, b := range w.pend {
			w.lits = append(w.lits, b)
			w.win.Put(b)
			w.mat.Insert(w.start)
			w.start++
		}
		w.sum = crc32.Update(w.sum, crc32.IEEETable, w.pend)
		w.total += uint64(len(w.pend))
		w.pend = nil
		w.flushLits()
	}
}

// Write 送入原文，返回值恒等于 len(p)。
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	if len(p) > 0 {
		w.dirty = true
	}
	w.pend = append(w.pend, p...)
	w.drain(false)
	return len(p), nil
}

// Bytes 返回当前已产生压缩字节的拷贝。
func (w *Writer) Bytes() []byte {
	b := make([]byte, len(w.out))
	copy(b, w.out)
	return b
}

// Flush 强制固化全部未决输入并写刷新标记；无新输入时不产生字节。
func (w *Writer) Flush() error {
	if w.closed {
		return ErrClosed
	}
	if !w.dirty {
		return nil
	}
	w.drain(true)
	w.out = wire.AppendUvarint(w.out, wire.TagFlush)
	w.dirty = false
	return nil
}

// Close 写出流尾；之后不可再 Write/Flush。
func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	w.drain(true)
	w.out = wire.AppendUvarint(w.out, wire.TagEnd)
	w.out = wire.AppendUvarint(w.out, w.total)
	w.out = wire.AppendUvarint(w.out, uint64(w.sum))
	w.closed = true
	return nil
}

// CompressParallel 切块并行压缩；同一切块方案对任意 workers 值输出相同。
func CompressParallel(data []byte, blockSize, workers, winCap, chainLimit int) ([]byte, error) {
	switch {
	case winCap < 3:
		return nil, ErrBadWindow
	case chainLimit < 1:
		return nil, ErrBadChainLimit
	case blockSize < 1:
		return nil, ErrBadBlockSize
	case workers < 1:
		return nil, ErrBadWorkers
	}
	n := (len(data) + blockSize - 1) / blockSize
	parts := make([][]byte, n)
	jobs := make(chan int, n)
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for j := range jobs {
			lo := j * blockSize
			hi := lo + blockSize
			if hi > len(data) {
				hi = len(data)
			}
			s := lo - winCap
			if s < 0 || lo == 0 {
				s = 0
			}
			parts[j] = runBlock(winCap, chainLimit, data[lo:hi], data[s:lo])
		}
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}
	for j := 0; j < n; j++ {
		jobs <- j
	}
	close(jobs)
	wg.Wait()
	out := wire.AppendHeader(nil, wire.Config{Window: winCap, ChainLimit: chainLimit})
	for _, p := range parts {
		out = append(out, p...)
	}
	out = wire.AppendUvarint(out, wire.TagEnd)
	out = wire.AppendUvarint(out, uint64(len(data)))
	out = wire.AppendUvarint(out, wire.Checksum(data))
	return out, nil
}
