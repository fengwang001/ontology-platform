// Package enc 提供流式压缩器（Write/Flush/Close）与按块并行压缩。
// 依赖 wire、match、window。单个 Encoder 实例不是并发安全的。
package enc

import (
	"errors"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// 并行压缩使用的默认参数。
const (
	DefaultWindow = 1 << 16
	DefaultChain  = 128
)

// ErrBadConfig 表示窗口容量或链长上限非法。
var ErrBadConfig = errors.New("enc: window and chain must be positive")

// Config 是压缩器配置。
type Config struct {
	Window int // 滑动窗口容量（最大回指距离）
	Chain  int // 哈希链候选上限
}

// Encoder 是流式压缩器。Write 只缓冲输入，解析推迟到 Flush/Close，
// 因此压缩结果与 Write 的切法无关（见 DESIGN.md 推导一）。
type Encoder struct {
	w      io.Writer
	data   []byte
	pos    int // 已解析到的位置
	m      *match.Matcher
	sum    uint64
	hdr    bool
	closed bool
	err    error
}

// New 创建压缩器；窗口或链长 <= 0 时返回 ErrBadConfig。
func New(w io.Writer, cfg Config) (*Encoder, error) {
	if cfg.Window <= 0 || cfg.Chain <= 0 {
		return nil, ErrBadConfig
	}
	win, err := window.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, cfg.Chain)
	if err != nil {
		return nil, err
	}
	return &Encoder{w: w, m: m, sum: wire.SumInit}, nil
}

// Write 缓冲输入，返回 len(p)。
func (e *Encoder) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if e.closed {
		return 0, errors.New("enc: write after close")
	}
	e.data = append(e.data, p...)
	for _, b := range p {
		e.sum = wire.SumByte(e.sum, b)
	}
	return len(p), nil
}

// Flush 把截至目前的全部输入压缩写出并追加刷新标记；
// 自上次 Flush 后没有新输入时不产生任何字节。
func (e *Encoder) Flush() error {
	if e.err != nil {
		return e.err
	}
	if e.pos == len(e.data) {
		return nil
	}
	out := e.header()
	e.m.SetData(e.data)
	out = parse(e.m, e.data, e.pos, len(e.data), out)
	e.pos = len(e.data)
	return e.emit(wire.AppendFlush(out))
}

// Close 压缩剩余输入并写出流尾。
func (e *Encoder) Close() error {
	if e.err != nil {
		return e.err
	}
	if e.closed {
		return nil
	}
	e.closed = true
	out := e.header()
	if e.pos < len(e.data) {
		e.m.SetData(e.data)
		out = parse(e.m, e.data, e.pos, len(e.data), out)
		e.pos = len(e.data)
	}
	return e.emit(wire.AppendEnd(out, uint64(len(e.data)), e.sum))
}

func (e *Encoder) header() []byte {
	if e.hdr {
		return nil
	}
	e.hdr = true
	return wire.AppendHeader(nil)
}

func (e *Encoder) emit(p []byte) error {
	if _, err := e.w.Write(p); err != nil {
		e.err = err
	}
	return e.err
}

// parse 对 data[start:end] 做贪心最长匹配解析，追加记录到 dst。
// 调用前需保证 [0, start) 已通过 Advance/LoadRange 进入历史。
func parse(m *match.Matcher, data []byte, start, end int, dst []byte) []byte {
	lit := start
	for p := start; p < end; {
		if dist, n := m.Longest(p); n > 0 {
			if p > lit {
				dst = wire.AppendLiteral(dst, data[lit:p])
			}
			dst = wire.AppendRef(dst, dist, n)
			for k := p; k < p+n; k++ {
				m.Advance(k)
			}
			p += n
			lit = p
		} else {
			m.Advance(p)
			p++
		}
	}
	if end > lit {
		dst = wire.AppendLiteral(dst, data[lit:end])
	}
	return dst
}

// CompressParallel 按 blockSize 切块并发压缩，每块以上一块末尾
// DefaultWindow 字节为预置字典。结果与 workers 无关、逐字节确定，
// 且是可被流式解压器直接解开的合法流。
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrBadConfig
	}
	nblk := (len(data) + blockSize - 1) / blockSize
	recs := make([][]byte, nblk)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				start := i * blockSize
				end := start + blockSize
				if end > len(data) {
					end = len(data)
				}
				win, _ := window.New(DefaultWindow)
				m, _ := match.New(win, DefaultChain)
				m.SetData(data[:end]) // 匹配不得越出本块边界
				ds := start - DefaultWindow
				if ds < 0 {
					ds = 0
				}
				m.LoadRange(ds, start)
				recs[i] = parse(m, data[:end], start, end, nil)
			}
		}()
	}
	for i := 0; i < nblk; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	out := wire.AppendHeader(nil)
	for _, r := range recs {
		out = append(out, r...)
	}
	return wire.AppendEnd(out, uint64(len(data)), wire.SumBytes(data)), nil
}
