package enc

import (
	"errors"
	"hash/fnv"
	"sync"

	"ontology/match"
	"ontology/wire"
	"ontology/window"
)

var (
	ErrBadConfig = errors.New("enc: window and chain must be > 0")
	ErrBadBlock  = errors.New("enc: blockSize and workers must be > 0")
	ErrClosed    = errors.New("enc: writer closed")
)

const (
	DefaultWindow = 1 << 15
	DefaultChain  = 64
)

// Writer 是流式压缩器。单实例非并发安全。
// 为保证输出与 Write 切法无关，输入缓存到 Flush/Close 再做确定贪心扫描。
type Writer struct {
	win    *window.Window
	m      *match.Matcher
	pend   []byte
	out    []byte
	sum    fnv.Hash64
	total  int
	hdr    bool
	closed bool
}

func NewWriter(windowCap, chain int) (*Writer, error) {
	if windowCap <= 0 || chain <= 0 {
		return nil, ErrBadConfig
	}
	win, err := window.New(windowCap)
	if err != nil {
		return nil, err
	}
	m, err := match.New(win, chain)
	if err != nil {
		return nil, err
	}
	return &Writer{win: win, m: m, sum: fnv.New64a()}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	w.pend = append(w.pend, p...)
	return len(p), nil
}

// encode 对一段确定字节做贪心 LZ77，追加令牌到 w.out。
func (w *Writer) encode(data []byte) {
	w.m.BeginBlock(data)
	litStart, i := 0, 0
	for i < len(data) {
		l, d := w.m.Find(i)
		if l > 0 {
			if litStart < i {
				w.out = wire.Literal(w.out, data[litStart:i])
			}
			w.out = wire.Match(w.out, d, l)
			end := i + l
			for ; i < end; i++ {
				w.m.Insert(i)
			}
			litStart = i
			continue
		}
		w.m.Insert(i)
		i++
	}
	if litStart < len(data) {
		w.out = wire.Literal(w.out, data[litStart:])
	}
}

func (w *Writer) Flush() error {
	if w.closed {
		return ErrClosed
	}
	if !w.hdr {
		w.out = append(w.out, wire.Header()...)
		w.hdr = true
	}
	if len(w.pend) == 0 {
		return nil
	}
	w.sum.Write(w.pend)
	w.total += len(w.pend)
	w.encode(w.pend)
	w.pend = w.pend[:0]
	w.out = wire.Flush(w.out)
	return nil
}

func (w *Writer) Close() ([]byte, error) {
	if w.closed {
		return nil, ErrClosed
	}
	if !w.hdr {
		w.out = append(w.out, wire.Header()...)
		w.hdr = true
	}
	if len(w.pend) > 0 {
		w.sum.Write(w.pend)
	w.total += len(w.pend)
		w.encode(w.pend)
		w.pend = w.pend[:0]
	}
	w.out = wire.End(w.out, uint64(w.total), w.sum.Sum64())
	w.closed = true
	return w.out, nil
}

// CompressParallel 按 blockSize 切块并行压缩；块 i 以上一块末尾窗口为预置字典。
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrBadBlock
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if nb == 0 {
		nb = 1
	}
	toks := make([][]byte, nb)
	jobs := make(chan int, nb)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			win, _ := window.New(DefaultWindow)
			m, _ := match.New(win, DefaultChain)
			for j := range jobs {
				lo, hi := j*blockSize, min((j+1)*blockSize, len(data))
				if j > 0 {
					win.Seed(data[max(0, lo-DefaultWindow):lo])
				}
				fw := &Writer{win: win, m: m}
				fw.pend = data[lo:hi]
				fw.encode(fw.pend)
				toks[j] = fw.out
			}
		}()
	}
	for j := range nb {
		jobs <- j
	}
	close(jobs)
	wg.Wait()

	sum := fnv.New64a()
	sum.Write(data)
	out := wire.Header()
	for j := range nb {
		out = append(out, toks[j]...)
	}
	return wire.End(out, uint64(len(data)), sum.Sum64()), nil
}
