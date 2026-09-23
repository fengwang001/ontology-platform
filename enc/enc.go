package enc

import (
	"errors"
	"hash"
	"hash/fnv"
	"io"
	"sync"

	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

const (
	DefaultWindow = 65536
	MinMatch      = 4
	MaxMatch      = 4096
	ChainLimit    = 64
)

var ErrInvalidConfig = errors.New("enc: invalid configuration")

type Config struct{ Window, ChainLimit int }

type Encoder struct {
	w       io.Writer
	buf     []byte
	decided int
	lit     int
	total   uint64
	last    int
	closed  bool
	head    bool
	sum     hash.Hash64
	win     *window.Window
	match   *match.Matcher
}

func New(w io.Writer, c Config) (*Encoder, error) {
	if c.Window == 0 {
		c.Window = DefaultWindow
	}
	if c.ChainLimit == 0 {
		c.ChainLimit = ChainLimit
	}
	win, err := window.New(c.Window)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	m, err := match.New(win, c.ChainLimit, MinMatch, MaxMatch)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return &Encoder{w: w, win: win, match: m, sum: fnv.New64()}, nil
}

func (e *Encoder) CandidatesExamined() int64 { return e.match.CandidatesExamined() }

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrInvalidConfig
	}
	if !e.head {
		if err := e.headOnce(); err != nil {
			return 0, err
		}
	}
	e.buf = append(e.buf, p...)
	_, _ = e.sum.Write(p)
	e.total += uint64(len(p))
	return len(p), e.encode(false)
}

func (e *Encoder) Flush() error {
	if e.closed {
		return nil
	}
	if !e.head {
		if err := e.headOnce(); err != nil {
			return err
		}
	}
	if err := e.encode(true); err != nil {
		return err
	}
	if e.last == len(e.buf) {
		return nil
	}
	if _, err := e.w.Write(wire.Flush(nil)); err != nil {
		return err
	}
	e.last = len(e.buf)
	return nil
}

func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	if !e.head {
		if err := e.headOnce(); err != nil {
			return err
		}
	}
	if err := e.encode(true); err != nil {
		return err
	}
	_, err := e.w.Write(wire.End(nil, e.total, e.sum.Sum64()))
	e.closed = true
	return err
}

func (e *Encoder) encode(all bool) error {
	limit := len(e.buf)
	if !all && len(e.buf) >= MaxMatch-1 {
		limit -= MaxMatch - 1
	} else if !all {
		limit = 0
	}
	e.match.Use(e.buf, e.decided)
	for e.decided < limit {
		d, l := e.match.Find(e.decided)
		if l < MinMatch {
			e.match.Advance(e.decided + 1)
			e.decided++
			continue
		}
		if l > len(e.buf)-e.decided {
			l = len(e.buf) - e.decided
		}
		if _, err := e.w.Write(wire.Literal(nil, e.buf[e.lit:e.decided])); err != nil {
			return err
		}
		if _, err := e.w.Write(wire.Match(nil, uint64(d), uint64(l))); err != nil {
			return err
		}
		end := e.decided + l
		e.match.Advance(end)
		e.decided, e.lit = end, end
	}
	return nil
}

func (e *Encoder) headOnce() error {
	if _, err := e.w.Write(wire.Header()); err != nil {
		return err
	}
	e.head = true
	return nil
}

func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrInvalidConfig
	}
	blocks := (len(data) + blockSize - 1) / blockSize
	out := make([][]byte, blocks)
	if workers > blocks && blocks > 0 {
		workers = blocks
	}
	jobs := make(chan int)
	go func() {
		for i := 0; i < blocks; i++ {
			jobs <- i
		}
		close(jobs)
	}()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i] = encodeBlock(data, i, blockSize)
			}
		}()
	}
	wg.Wait()
	dst := wire.Header()
	for _, b := range out {
		dst = append(dst, b...)
	}
	sum := fnv.New64()
	_, _ = sum.Write(data)
	return wire.End(dst, uint64(len(data)), sum.Sum64()), nil
}

func encodeBlock(data []byte, index, size int) []byte {
	start := index * size
	end := start + size
	if end > len(data) {
		end = len(data)
	}
	dictStart := start - DefaultWindow
	if dictStart < 0 {
		dictStart = 0
	}
	src := data[dictStart:end]
	history := start - dictStart
	win, _ := window.New(DefaultWindow)
	m, _ := match.New(win, ChainLimit, MinMatch, MaxMatch)
	m.Use(src, history)
	dst, lit := []byte{}, history
	for pos := history; pos < len(src); {
		d, l := m.Find(pos)
		if l < MinMatch {
			m.Advance(pos + 1)
			pos++
			continue
		}
		if l > len(src)-pos {
			l = len(src) - pos
		}
		dst = wire.Literal(dst, src[lit:pos])
		dst = wire.Match(dst, uint64(d), uint64(l))
		pos += l
		m.Advance(pos)
		lit = pos
	}
	return wire.Literal(dst, src[lit:])
}
