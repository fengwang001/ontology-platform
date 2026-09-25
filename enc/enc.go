// Package enc 实现自定 LZ77 格式的流式压缩与按块并行压缩。
// 单个 Compressor 实例不是并发安全的；并行性仅由 CompressParallel 提供。
package enc

import (
	"errors"
	"sync"

	"ontology/match"
	"ontology/wire"
	"ontology/window"
)

const (
	// DefaultWindow 是默认窗口容量。
	DefaultWindow = 1 << 15
	// DefaultChain 是默认候选链长度上限。
	DefaultChain = 32
)

// ErrInvalidConfig 在窗口或链长配置非法时返回。
var ErrInvalidConfig = errors.New("enc: invalid configuration")

// Compressor 是流式压缩器：Write 只缓存，Flush/Close 才产出确定结果。
type Compressor struct {
	out      []byte
	pending  []byte
	win      *window.Window
	mat      *match.Matcher
	total    uint64
	sum      *wire.Hasher
	closed   bool
	headDone bool
}

// NewCompressor 以默认配置构造压缩器。
func NewCompressor() (*Compressor, error) {
	return NewCompressorConfig(DefaultWindow, DefaultChain)
}

// NewCompressorConfig 以给定窗口容量与链长上限构造压缩器。
func NewCompressorConfig(windowCap, maxChain int) (*Compressor, error) {
	w, err := window.New(windowCap)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	mt, err := match.New(w, maxChain)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return &Compressor{win: w, mat: mt, sum: wire.NewHasher()}, nil
}

func (c *Compressor) ensureHead() {
	if !c.headDone {
		c.out = wire.AppendHeader(c.out)
		c.headDone = true
	}
}

// Write 只缓存输入，不产出压缩记录，保证结果与切法无关。
func (c *Compressor) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("enc: write after close")
	}
	c.pending = append(c.pending, p...)
	return len(p), nil
}

// Flush 强制排空全部未决输入并追加刷新标记；
// 连续两次 Flush 间没有新输入时不产生任何字节。
func (c *Compressor) Flush() error {
	if c.closed {
		return errors.New("enc: flush after close")
	}
	if len(c.pending) == 0 {
		return nil
	}
	c.ensureHead()
	c.out = append(c.out, wire.TagFlush)
	return c.drain()
}

func (c *Compressor) drain() error {
	out, n := encodeBlock(c.mat, c.out, c.pending, nil)
	c.out = out
	c.total += uint64(n)
	_, _ = c.sum.Write(c.pending)
	c.pending = c.pending[:0]
	return nil
}

// Close 排空输入并写入流尾。
func (c *Compressor) Close() error {
	if c.closed {
		return errors.New("enc: double close")
	}
	c.closed = true
	c.ensureHead()
	if err := c.drain(); err != nil {
		return err
	}
	c.out = append(c.out, wire.TagEnd)
	c.out = wire.AppendUvarint(c.out, c.total)
	c.out = wire.AppendUvarint(c.out, c.sum.Sum64())
	return nil
}

// Bytes 返回已产出的压缩字节。
func (c *Compressor) Bytes() []byte { return c.out }

// encodeBlock 在给定匹配器状态上编码 block；dict 为预置字典（可为 nil），
// 仅用于匹配比对，不重新进入窗口。返回新输出与编码字节数。
func encodeBlock(mt *match.Matcher, dst []byte, block, dict []byte) ([]byte, int) {
	hist := block
	if dict != nil {
		hist = append(append(make([]byte, 0, len(dict)+len(block)), dict...), block...)
	}
	base := len(hist) - len(block)
	litStart := 0
	emitLit := func(end int) {
		if end <= litStart {
			return
		}
		dst = append(dst, wire.TagLiteral)
		dst = wire.AppendUvarint(dst, uint64(end-litStart))
		dst = append(dst, block[litStart:end]...)
		litStart = end
	}
	i := 0
	for i < len(block) {
		cur := base + i
		d, l := mt.Find(hist, cur)
		if d == 0 {
			b := hist[cur]
			if i < 2 {
				mt.AddByte(b)
			} else {
				mt.Insert3(cur, hist[cur-2], hist[cur-1], b)
			}
			i++
			continue
		}
		emitLit(i)
		dst = append(dst, wire.TagMatch)
		dst = wire.AppendUvarint(dst, uint64(d))
		dst = wire.AppendUvarint(dst, uint64(l))
		for k := 0; k < l; k++ {
			cc := cur + k
			b := hist[cc]
			if cc < 2 {
				mt.AddByte(b)
			} else {
				mt.Insert3(cc, hist[cc-2], hist[cc-1], b)
			}
		}
		i += l
		litStart = i
	}
	emitLit(len(block))
	return dst, len(block)
}

// CompressParallel 按 blockSize 切块并行压缩。每块可回指上一块末尾
// 至多一个窗口容量的字节；结果是单个合法流，与 workers 取值无关。
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrInvalidConfig
	}
	if len(data) == 0 {
		c, err := NewCompressor()
		if err != nil {
			return nil, err
		}
		_ = c.Close()
		return c.Bytes(), nil
	}
	if blockSize > len(data) {
		blockSize = len(data)
	}
	nb := (len(data) + blockSize - 1) / blockSize
	chunks := make([][]byte, nb)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for bi := 0; bi < nb; bi++ {
		lo, hi := bi*blockSize, (bi+1)*blockSize
		if hi > len(data) {
			hi = len(data)
		}
		dlo := lo - DefaultWindow
		if dlo < 0 {
			dlo = 0
		}
		wg.Add(1)
		go func(bi, lo, hi, dlo int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			w, _ := window.New(DefaultWindow)
			mt, _ := match.New(w, DefaultChain)
			dict := data[dlo:lo]
			hist := append(append(make([]byte, 0, lo-dlo+hi-lo), dict...), data[lo:hi]...)
			for j, b := range dict {
				if j < 2 {
					mt.AddByte(b)
				} else {
					mt.Insert3(j, hist[j-2], hist[j-1], b)
				}
			}
			var out []byte
			out, _ = encodeBlock(mt, out, hist[len(dict):], dict)
			if bi < nb-1 {
				out = append(out, wire.TagFlush)
			}
			chunks[bi] = out
		}(bi, lo, hi, dlo)
	}
	wg.Wait()
	res := wire.AppendHeader(nil)
	for _, ch := range chunks {
		res = append(res, ch...)
	}
	h := wire.NewHasher()
	_, _ = h.Write(data)
	res = append(res, wire.TagEnd)
	res = wire.AppendUvarint(res, uint64(len(data)))
	res = wire.AppendUvarint(res, h.Sum64())
	return res, nil
}
